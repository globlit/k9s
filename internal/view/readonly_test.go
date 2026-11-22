// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/expect"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// mutationWords name actions that change the cluster or local state.
var mutationWords = []string{
	"delete", "kill", "edit", "scale", "restart", "set image", "drain", "cordon",
	"rollback", "roll back", "trigger", "suspend", "apply", "shell", "attach", "transfer",
	"sanitize", "uninstall", "reconcile", "force/reset",
}

// roKnown lists actions that stay bound in read-only mode. They do not change
// the cluster. Each one is reported as a finding.
var roKnown = map[string]string{
	"portforward|Delete": "stops a local port-forward",
}

type roView struct {
	name string
	cmd  string
	// enter drills down with Enter after cmd, for example pod to containers.
	enter string
}

var roViews = []roView{
	{name: "pods", cmd: "pods"},
	{name: "containers", cmd: "pods", enter: "ns1/pod-a"},
	{name: "deployments", cmd: "deploy"},
	{name: "statefulsets", cmd: "sts"},
	{name: "daemonsets", cmd: "ds"},
	{name: "replicasets", cmd: "rs"},
	{name: "services", cmd: "svc"},
	{name: "nodes", cmd: "nodes"},
	{name: "namespaces", cmd: "ns"},
	{name: "secrets", cmd: "secrets"},
	{name: "configmaps", cmd: "cm"},
	{name: "serviceaccounts", cmd: "sa"},
	{name: "pvcs", cmd: "pvc"},
	{name: "pvs", cmd: "pv"},
	{name: "priorityclasses", cmd: "pc"},
	{name: "events", cmd: "events"},
	{name: "cronjobs", cmd: "cj"},
	{name: "jobs", cmd: "jobs"},
	{name: "crds", cmd: "crds"},
	{name: "clusterroles", cmd: "clusterroles"},
	{name: "roles", cmd: "roles"},
	{name: "helm", cmd: "helm"},
	{name: "contexts", cmd: "ctx"},
	{name: "portforward", cmd: "pf"},
	{name: "screendump", cmd: "sd"},
	{name: "benchmark", cmd: "bench"},
	{name: "aliases", cmd: "aliases"},
	{name: "workloads", cmd: "wk"},
	{name: "xray", cmd: "xray deploy"},
	{name: "pulse", cmd: "pulse"},
	{name: "widgets", cmd: "widgets"},
	{name: "kustomizations", cmd: "kustomizations"},
	{name: "helmreleases", cmd: "helmreleases"},
	{name: "dir", cmd: "dir %s"},
}

var (
	roWidgetGVR = client.NewGVR("example.com/v1/widgets")
	roKsGVR     = client.NewGVR("kustomize.toolkit.fluxcd.io/v1/kustomizations")
	roHrGVR     = client.NewGVR("helm.toolkit.fluxcd.io/v2/helmreleases")
)

func roObjects() []runtime.Object {
	cr := func(gvr *client.GVR, kind, n string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": gvr.GV().String(), "kind": kind,
			"metadata": map[string]any{"namespace": "ns1", "name": n},
		}}
	}
	return []runtime.Object{
		simPod("ns1", "pod-a"), mutDeploy("dp-a", 1), simNode("n1"),
		&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "cj-a"}, Spec: batchv1.CronJobSpec{Schedule: "* * * * *"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "cm-a"}},
		cr(roWidgetGVR, "Widget", "w1"), cr(roKsGVR, "Kustomization", "k1"), cr(roHrGVR, "HelmRelease", "h1"),
	}
}

func roCluster(t *testing.T) *fakecluster.Cluster {
	t.Helper()
	return fakecluster.New(t,
		fakecluster.WithCRD(roWidgetGVR, "Widget", true),
		fakecluster.WithCRD(roKsGVR, "Kustomization", true),
		fakecluster.WithCRD(roHrGVR, "HelmRelease", true),
		fakecluster.WithObjects(roObjects()...),
	)
}

func keyName(k tcell.Key) string {
	if n, ok := tcell.KeyNames[k]; ok {
		return n
	}
	return fmt.Sprintf("key-%d", k)
}

type boundAction struct {
	key, desc string
	danger    bool
}

func boundActions(t *testing.T, s *SimApp) []boundAction {
	t.Helper()
	v := topViewer(t, s)
	var out []boundAction
	s.onLoop(func() {
		v.Actions().Range(func(k tcell.Key, a ui.KeyAction) {
			out = append(out, boundAction{key: keyName(k), desc: a.Description, danger: a.Opts.Dangerous})
		})
	})
	slices.SortFunc(out, func(a, b boundAction) int { return strings.Compare(a.key, b.key) })

	return out
}

func formatActions(aa []boundAction) string {
	var b strings.Builder
	for _, a := range aa {
		d := ""
		if a.danger {
			d = " [dangerous]"
		}
		fmt.Fprintf(&b, "%-12s %s%s\n", a.key, a.desc, d)
	}

	return b.String()
}

func openRoView(t *testing.T, s *SimApp, v roView, dir string) {
	t.Helper()
	cmd := v.cmd
	if strings.Contains(cmd, "%s") {
		cmd = fmt.Sprintf(cmd, dir)
	}
	before := s.TopView()
	s.Cmd(":" + cmd)
	if v.enter != "" {
		selectPath(t, s, v.enter)
		s.Keys(tcell.KeyEnter)
	}
	s.Eventually(func() bool {
		_, ok := s.app.Content.Top().(ResourceViewer)
		return ok && (s.TopView() != before || v.cmd == "pods")
	}, "view "+v.name+" not shown")
}

// stableActions waits until the bound actions stop changing. Views bind
// more keys after the first refresh.
func stableActions(t *testing.T, s *SimApp) []boundAction {
	t.Helper()
	var (
		last   string
		same   int
		actual []boundAction
	)
	for range 400 {
		actual = boundActions(t, s)
		cur := formatActions(actual)
		if cur == last {
			same++
		} else {
			same = 0
		}
		if same == 25 {
			return actual
		}
		last = cur
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("actions did not settle")

	return nil
}

func TestReadOnlyBindings(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("x: 1\n"), 0o600))

	for _, ro := range []bool{false, true} {
		mode := "rw"
		opts := []SimOption{SimNamespace("ns1")}
		if ro {
			mode = "ro"
			opts = append(opts, SimReadOnly())
		}
		s := NewSimApp(t, roCluster(t), opts...)
		for _, v := range roViews {
			t.Run(mode+"/"+v.name, func(t *testing.T) {
				openRoView(t, s, v, dir)
				aa := stableActions(t, s)

				expect.Match(t, filepath.Join("readonly", mode, v.name+".txt"), formatActions(aa))
				for _, c := range s.fc.PseudoCalls() {
					t.Logf("API call on pseudo resource: %s %s", c.Action.GetVerb(), c.Action.GetResource().Resource)
				}
				s.fc.Reset()
				if !ro {
					return
				}
				for _, a := range aa {
					if _, ok := roKnown[v.name+"|"+a.desc]; ok {
						continue
					}
					assert.False(t, a.danger, "dangerous action %q on %s in read-only", a.desc, a.key)
					low := strings.ToLower(a.desc)
					for _, w := range mutationWords {
						assert.NotContains(t, low, w, "mutating action %q on %s in read-only", a.desc, a.key)
					}
				}
			})
		}
	}
}

// TestReadOnlyKeysDoNothing presses every mutating key in read-only mode and
// checks the cluster sees no write.
func TestReadOnlyKeysDoNothing(t *testing.T) {
	fc := roCluster(t)
	s := NewSimApp(t, fc, SimNamespace("ns1"), SimReadOnly())
	keys := []any{
		tcell.KeyCtrlD, tcell.KeyCtrlK, tcell.KeyCtrlL, ui.KeyS, ui.KeyE, ui.KeyR, ui.KeyT,
		ui.KeyC, ui.KeyU, ui.KeyZ, ui.KeyI, ui.KeyA, ui.KeyShiftR, ui.KeyShiftT, ui.KeyShiftW, ui.KeyShiftF,
	}
	views := []struct{ cmd, sel string }{
		{"pods", "ns1/pod-a"}, {"deploy", "ns1/dp-a"}, {"nodes", "n1"}, {"cj", "ns1/cj-a"},
		{"cm", "ns1/cm-a"},
	}
	for _, v := range views {
		s.Cmd(":" + v.cmd)
		selectPath(t, s, v.sel)
		fc.Reset()
		for _, k := range keys {
			s.Keys(k)
			for i := 0; i < 3 && s.DialogOpen(); i++ {
				s.Keys(tcell.KeyTab, tcell.KeyEnter)
			}
			s.Keys(tcell.KeyEscape)
			s.Cmd(":" + v.cmd)
			selectPath(t, s, v.sel)
		}
		for _, verb := range []string{"create", "update", "patch", "delete"} {
			assert.Empty(t, fc.Actions(verb, "*"), "%s on %s in read-only", verb, v.cmd)
		}
	}
}
