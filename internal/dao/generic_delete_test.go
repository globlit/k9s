// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var errMut = errors.New("boom")

// mutConn wraps a connection and fails the calls that have an error set.
type mutConn struct {
	client.Connection

	canIErr, dialErr, dynErr error
}

func (c *mutConn) CanI(ns string, gvr *client.GVR, n string, verbs []string) (bool, error) {
	if c.canIErr != nil {
		return false, c.canIErr
	}
	return c.Connection.CanI(ns, gvr, n, verbs)
}

func (c *mutConn) Dial() (kubernetes.Interface, error) {
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	return c.Connection.Dial()
}

func (c *mutConn) DynDial() (dynamic.Interface, error) {
	if c.dynErr != nil {
		return nil, c.dynErr
	}
	return c.Connection.DynDial()
}

func mutFactory(t *testing.T, conn client.Connection) *watch.Factory {
	t.Helper()
	f := watch.NewFactory(conn)
	t.Cleanup(f.Terminate)

	return f
}

// mutCluster returns a cluster with a switch that denies all verbs.
func mutCluster(t *testing.T, opts ...fakecluster.Option) (*fakecluster.Cluster, *atomic.Bool) {
	t.Helper()
	var deny atomic.Bool
	opts = append(opts, fakecluster.WithRBAC(func(string, *client.GVR, string, string) bool {
		return !deny.Load()
	}))

	return fakecluster.New(t, opts...), &deny
}

func mutObj(gvr *client.GVR, kind, ns, name string) *unstructured.Unstructured {
	u := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvr.GV().String(),
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
	}}
	if ns != "" {
		u.SetNamespace(ns)
	}

	return &u
}

// mutEnsure adds o back when an earlier case deleted it.
func mutEnsure(t *testing.T, fc *fakecluster.Cluster, gvr *client.GVR, o *unstructured.Unstructured) {
	t.Helper()
	if _, err := fc.DynGet(gvr, o.GetNamespace(), o.GetName()); err != nil {
		fc.Add(o)
	}
}

type deleteCase struct {
	name  string
	prop  *metav1.DeletionPropagation
	grace Grace
	deny  bool
}

func deleteCases() []deleteCase {
	props := map[string]*metav1.DeletionPropagation{
		"nil":        nil,
		"background": new(metav1.DeletePropagationBackground),
		"foreground": new(metav1.DeletePropagationForeground),
		"orphan":     new(metav1.DeletePropagationOrphan),
	}
	graces := map[string]Grace{"default": DefaultGrace, "force": ForceGrace, "now": NowGrace}
	var cc []deleteCase
	for pn, p := range props {
		for gn, g := range graces {
			for _, deny := range []bool{false, true} {
				cc = append(cc, deleteCase{
					name:  fmt.Sprintf("%s-%s-deny=%t", pn, gn, deny),
					prop:  p,
					grace: g,
					deny:  deny,
				})
			}
		}
	}

	return cc
}

func expGrace(g Grace) *int64 {
	if g == DefaultGrace {
		return nil
	}
	v := int64(g)
	return &v
}

// runDeleteMatrix runs every delete case on path and checks the API call.
func runDeleteMatrix(t *testing.T, fc *fakecluster.Cluster, deny *atomic.Bool, gvr *client.GVR,
	o *unstructured.Unstructured, path string, del func(context.Context, string, *metav1.DeletionPropagation, Grace) error) {
	t.Helper()
	for _, u := range deleteCases() {
		t.Run(u.name, func(t *testing.T) {
			mutEnsure(t, fc, gvr, o)
			deny.Store(u.deny)
			t.Cleanup(func() { deny.Store(false) })
			fc.Reset()

			err := del(context.Background(), path, u.prop, u.grace)
			if u.deny {
				require.ErrorContains(t, err, "not authorized")
				assert.Empty(t, fc.Actions("delete", "*"))
				assert.Positive(t, fc.CanICalls())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []fakecluster.DeleteCall{{
				GVR:         gvr.GVR(),
				NS:          o.GetNamespace(),
				Name:        o.GetName(),
				Propagation: u.prop,
				Grace:       expGrace(u.grace),
				Source:      fakecluster.SourceDyn,
			}}, fc.Deletes())
			_, err = fc.DynGet(gvr, o.GetNamespace(), o.GetName())
			require.Error(t, err)
		})
	}
}

func TestGenericDeleteMatrix(t *testing.T) {
	nsGVR := client.NewGVR("example.com/v1/widgets")
	clGVR := client.NewGVR("example.com/v1/gadgets")
	uu := map[string]struct {
		gvr  *client.GVR
		kind string
		ns   string
		path string
	}{
		"namespaced":         {gvr: nsGVR, kind: "Widget", ns: "ns1", path: "ns1/w1"},
		"cluster-dash":       {gvr: clGVR, kind: "Gadget", path: "-/g1"},
		"cluster-plain-name": {gvr: clGVR, kind: "Gadget", path: "g1"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			_, n := client.Namespaced(u.path)
			o := mutObj(u.gvr, u.kind, u.ns, n)
			fc, deny := mutCluster(t,
				fakecluster.WithCRD(nsGVR, "Widget", true),
				fakecluster.WithCRD(clGVR, "Gadget", false),
			)
			var g Generic
			g.Init(mutFactory(t, fc.Conn()), u.gvr)

			runDeleteMatrix(t, fc, deny, u.gvr, o, u.path, g.Delete)
		})
	}
}

func TestResourceDeleteMatrix(t *testing.T) {
	uu := map[string]struct {
		gvr  *client.GVR
		kind string
		ns   string
	}{
		"pod":       {gvr: client.PodGVR, kind: "Pod", ns: "ns1"},
		"dp":        {gvr: client.DpGVR, kind: "Deployment", ns: "ns1"},
		"ds":        {gvr: client.DsGVR, kind: "DaemonSet", ns: "ns1"},
		"sts":       {gvr: client.StsGVR, kind: "StatefulSet", ns: "ns1"},
		"rs":        {gvr: client.RsGVR, kind: "ReplicaSet", ns: "ns1"},
		"job":       {gvr: client.JobGVR, kind: "Job", ns: "ns1"},
		"cronjob":   {gvr: client.CjGVR, kind: "CronJob", ns: "ns1"},
		"pvc":       {gvr: client.PvcGVR, kind: "PersistentVolumeClaim", ns: "ns1"},
		"secret":    {gvr: client.SecGVR, kind: "Secret", ns: "ns1"},
		"cm":        {gvr: client.CmGVR, kind: "ConfigMap", ns: "ns1"},
		"svc":       {gvr: client.SvcGVR, kind: "Service", ns: "ns1"},
		"crd":       {gvr: client.CrdGVR, kind: "CustomResourceDefinition"},
		"namespace": {gvr: client.NsGVR, kind: "Namespace"},
		"node":      {gvr: client.NodeGVR, kind: "Node"},
		"pv":        {gvr: client.PvGVR, kind: "PersistentVolume"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			o := mutObj(u.gvr, u.kind, u.ns, "o1")
			if u.gvr == client.CrdGVR {
				o.SetName("things.example.com")
			}
			fc, deny := mutCluster(t)
			a, err := AccessorFor(mutFactory(t, fc.Conn()), u.gvr)
			require.NoError(t, err)
			nuker, ok := a.(Nuker)
			require.True(t, ok, "%T is not a nuker", a)

			path := client.FQN(u.ns, o.GetName())
			runDeleteMatrix(t, fc, deny, u.gvr, o, path, nuker.Delete)
		})
	}
}

func TestWorkloadDeleteMatrix(t *testing.T) {
	uu := map[string]struct {
		gvr  *client.GVR
		kind string
		ns   string
	}{
		"pod":  {gvr: client.PodGVR, kind: "Pod", ns: "ns1"},
		"dp":   {gvr: client.DpGVR, kind: "Deployment", ns: "ns1"},
		"svc":  {gvr: client.SvcGVR, kind: "Service", ns: "ns1"},
		"node": {gvr: client.NodeGVR, kind: "Node"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			o := mutObj(u.gvr, u.kind, u.ns, "o1")
			fc, deny := mutCluster(t)
			var w Workload
			w.Init(mutFactory(t, fc.Conn()), client.WkGVR)
			del := func(ctx context.Context, p string, prop *metav1.DeletionPropagation, g Grace) error {
				return w.Delete(context.WithValue(ctx, internal.KeyGVR, u.gvr), p, prop, g)
			}

			path := client.FQN(u.ns, o.GetName())
			if u.ns == "" {
				path = "-/" + o.GetName()
			}
			runDeleteMatrix(t, fc, deny, u.gvr, o, path, del)
		})
	}
}

// TestDeleteErrors checks each nuker returns API and client errors.
func TestDeleteErrors(t *testing.T) {
	type nukeFn func(f Factory, gvr *client.GVR) func(context.Context, string, *metav1.DeletionPropagation, Grace) error
	generic := func(f Factory, gvr *client.GVR) func(context.Context, string, *metav1.DeletionPropagation, Grace) error {
		var g Generic
		g.Init(f, gvr)
		return g.Delete
	}
	workload := func(f Factory, gvr *client.GVR) func(context.Context, string, *metav1.DeletionPropagation, Grace) error {
		var w Workload
		w.Init(f, client.WkGVR)
		return func(ctx context.Context, p string, prop *metav1.DeletionPropagation, g Grace) error {
			return w.Delete(context.WithValue(ctx, internal.KeyGVR, gvr), p, prop, g)
		}
	}
	nukers := map[string]nukeFn{"generic": generic, "workload": workload}
	uu := map[string]struct {
		opts []fakecluster.Option
		conn func(client.Connection) client.Connection
		path string
		err  string
	}{
		"api-error": {
			opts: []fakecluster.Option{fakecluster.FailOn("delete", "pods", 0, errMut)},
			path: "ns1/p1",
			err:  "boom",
		},
		"not-found": {path: "ns1/nope", err: "not found"},
		"cluster-api-error": {
			opts: []fakecluster.Option{fakecluster.FailOn("delete", "pods", 0, errMut)},
			path: "-/p1",
			err:  "boom",
		},
		"cani-error": {
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, canIErr: errMut} },
			path: "ns1/p1",
			err:  "boom",
		},
		"dyn-dial-error": {
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dynErr: errMut} },
			path: "ns1/p1",
			err:  "boom",
		},
	}

	for nk, nf := range nukers {
		for k, u := range uu {
			t.Run(nk+"/"+k, func(t *testing.T) {
				opts := append([]fakecluster.Option{fakecluster.WithObjects(phasePod("p1", "Running"))}, u.opts...)
				fc := fakecluster.New(t, opts...)
				var conn client.Connection = fc.Conn()
				if u.conn != nil {
					conn = u.conn(conn)
				}
				del := nf(mutFactory(t, conn), client.PodGVR)

				var err error
				require.NotPanics(t, func() {
					err = del(context.Background(), u.path, nil, DefaultGrace)
				})
				require.ErrorContains(t, err, u.err)
			})
		}
	}
}
