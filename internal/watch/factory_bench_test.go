// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package watch_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

func BenchmarkFactoryList(b *testing.B) {
	const n = 5_000
	oo := make([]runtime.Object, 0, n)
	now := time.Now()
	for i := range n {
		oo = append(oo, newPod("ns1", fmt.Sprintf("p-%d", i), now))
	}
	fc := fakecluster.New(b, fakecluster.WithNamespaces("ns1"), fakecluster.WithObjects(oo...))
	f := watch.NewFactory(fc.Conn())
	f.Start("ns1")
	b.Cleanup(f.Terminate)
	if l, err := f.List(client.PodGVR, "ns1", true, labels.Everything()); err != nil || len(l) != n {
		b.Fatalf("list: got %d objects, err %v", len(l), err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := f.List(client.PodGVR, "ns1", false, labels.Everything()); err != nil {
			b.Fatal(err)
		}
	}
}
