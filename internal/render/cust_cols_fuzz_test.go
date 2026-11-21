// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/model1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// loopRx finds jq builtins that loop or grow without bound on purpose.
var loopRx = regexp.MustCompile(`repeat|range|recurse|while|until|limit|def|input|env|\.\.|getpath|paths|walk|splits|combinations|\*|pow|exp|tostream|fromstream|@`)

var custColSeeds = []string{
	"",
	"a",
	"fred|W",
	"fred|WH",
	"AGE|TR",
	"b:.metadata.name",
	"b:.metadata.name|NW",
	"b:.metadata.name|T",
	"b:{{crap.bozo}}|NW",
	"BLEE:.spec.addresses[?(@.type == 'CiliumInternalIP')].ip|WR",
	"fred||.metadata.name|W",
	":.metadata.name.fred|TW",
	"LAST SEEN:.status.conditions[4].lastHeartbeatTime",
	"START:.status.startTime|T",
	"C:.spec.containers[*].name",
	"C:.spec.containers[0:2].image",
	"C:.spec.containers[-1].name",
	"JQ:|.metadata.labels | keys | join(\",\")|",
	"JQ:|.spec.containers | length|",
	"JQ:|.metadata.name|",
	"R:.spec.containers[*].resources.requests.cpu",
}

func fuzzObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":              "fred",
			"namespace":         "default",
			"creationTimestamp": "2026-04-22T12:00:00Z",
			"labels":            map[string]any{"app": "fred", "tier": "be"},
		},
		"spec": map[string]any{
			"addresses": []any{map[string]any{"type": "CiliumInternalIP", "ip": "10.0.0.1"}},
			"containers": []any{
				map[string]any{"name": "c1", "image": "nginx", "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "1Gi"}}},
				map[string]any{"name": "c2", "image": "busybox"},
			},
		},
		"status": map[string]any{
			"startTime":  "2026-04-22T12:00:00Z",
			"conditions": []any{map[string]any{"lastHeartbeatTime": "2026-04-22T12:00:00Z"}},
		},
	}}
}

func FuzzCustomColumns(f *testing.F) {
	for _, s := range custColSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		cc, err := NewColsSpecs(s, "NAME", "AGE:.metadata.creationTimestamp|T").parseSpecs()
		if err != nil {
			return
		}
		for _, c := range cc {
			if isJQSpec(c.Spec) && loopRx.MatchString(c.Spec) {
				return
			}
		}
		rh := model1.Header{{Name: "NAME"}, {Name: "STATUS"}, {Name: "AGE"}}
		row := model1.Row{ID: "default/fred", Fields: model1.Fields{"fred", "Running", "1m"}}
		cc.Header(rh)

		runWithin(t, 2*time.Second, func() {
			rr, _ := cc.realize(fuzzObject(), rh, &row)
			if len(rr) < len(cc) {
				t.Errorf("got %d columns, want at least %d", len(rr), len(cc))
			}
		})
	})
}

func FuzzJQParse(f *testing.F) {
	for _, s := range custColSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if loopRx.MatchString(s) || !isJQSpec(s) {
			return
		}
		runWithin(t, 2*time.Second, func() {
			compileSpec("fuzz", s)
			jqParse(s, fuzzObject().Object)
		})
	})
}

func FuzzFormatColValue(f *testing.F) {
	for _, s := range []string{
		"", "100m", "1Gi", "1.5", "-1", "2026-04-22T12:00:00Z", "bozo", "1e3", "9Ei", "1e400",
	} {
		f.Add(s, uint8(0))
		f.Add(s, uint8(1))
		f.Add(s, uint8(2))
	}

	f.Fuzz(func(t *testing.T, s string, mode uint8) {
		var h model1.HeaderColumn
		switch mode % 4 {
		case 1:
			h.MXC = true
		case 2:
			h.MXM = true
		case 3:
			h.Time = true
		}
		if formatColValue(h, s) == "" && s != "" {
			t.Fatalf("empty value for %q", s)
		}
	})
}

func runWithin(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Sprintf("panic: %v\n%s", r, debug.Stack())
				return
			}
			done <- nil
		}()
		fn()
	}()
	select {
	case r := <-done:
		if r != nil {
			t.Fatal(r)
		}
	case <-time.After(d):
		t.Fatalf("no result after %v", d)
	}
}
