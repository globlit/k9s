// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"fmt"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BenchmarkGenericDeleteMarks runs the DAO part of a Browser delete of 500
// marks. Seed the objects again before each loop, outside the timer.
func BenchmarkGenericDeleteMarks(b *testing.B) {
	const n = 500
	fc := fakecluster.New(b, fakecluster.WithNamespaces("ns1"))
	f := watch.NewFactory(fc.Conn())
	b.Cleanup(f.Terminate)
	var g Generic
	g.Init(f, client.PodGVR)

	sels := make([]string, 0, n)
	for i := range n {
		sels = append(sels, fmt.Sprintf("ns1/p-%d", i))
	}
	pods := fc.Dyn().Resource(client.PodGVR.GVR()).Namespace("ns1")
	seed := func() {
		fc.Reset()
		for i := range n {
			u := unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"namespace": "ns1", "name": fmt.Sprintf("p-%d", i)},
			}}
			if _, err := pods.Create(context.Background(), &u, metav1.CreateOptions{}); err != nil {
				b.Fatal(err)
			}
		}
	}
	bg := metav1.DeletePropagationBackground

	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		seed()
		b.StartTimer()
		for _, sel := range sels {
			if err := g.Delete(context.Background(), sel, &bg, DefaultGrace); err != nil {
				b.Fatal(err)
			}
		}
	}
}
