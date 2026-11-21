// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

// Package fakecluster provides an in-memory cluster behind a client.Connection.
package fakecluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/globlit/k9s/internal/client"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	mxv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	mxfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// DefaultContext is the context name used when WithContext is not given.
const DefaultContext = "fake"

// Policy decides if verb is allowed on a resource.
type Policy func(ns string, gvr *client.GVR, name, verb string) bool

// Option configures a Cluster.
type Option func(*Cluster)

// Cluster is an in-memory cluster with typed, dynamic and metrics fakes.
type Cluster struct {
	t testing.TB

	typed *fake.Clientset
	dyn   *dynfake.FakeDynamicClient
	mx    *mxfake.Clientset
	disc  *cachedDiscovery
	cfg   *client.Config
	conn  *Conn

	resources  []Resource
	byGVK      map[schema.GroupVersionKind]Resource
	objects    []runtime.Object
	metrics    []runtime.Object
	namespaces []string
	contexts   []string
	context    string
	hasMetrics bool
	mirror     bool
	policies   []Policy
	failures   []*failure

	connected atomic.Bool
	canICalls atomic.Int64

	recMx sync.Mutex
	calls []Call
}

// WithObjects seeds objects. Typed objects go to the typed and dynamic stores.
// Unstructured objects go to the dynamic store, and to the typed store when
// the kind is known.
func WithObjects(oo ...runtime.Object) Option {
	return func(c *Cluster) { c.objects = append(c.objects, oo...) }
}

// WithNamespaces adds namespaces. The default namespace always exists.
func WithNamespaces(nn ...string) Option {
	return func(c *Cluster) { c.namespaces = append(c.namespaces, nn...) }
}

// WithMetrics turns on metrics and seeds PodMetrics or NodeMetrics objects.
func WithMetrics(oo ...runtime.Object) Option {
	return func(c *Cluster) {
		c.hasMetrics = true
		c.metrics = append(c.metrics, oo...)
	}
}

// WithContext adds a kubeconfig context and makes it the active one.
func WithContext(name string) Option {
	return func(c *Cluster) {
		c.contexts = append(c.contexts, name)
		c.context = name
	}
}

// WithCRD serves a custom resource and adds its CRD object.
func WithCRD(gvr *client.GVR, kind string, namespaced bool) Option {
	return func(c *Cluster) {
		c.resources = append(c.resources, Resource{GVR: gvr, Kind: kind, Namespaced: namespaced})
		scope := "Cluster"
		if namespaced {
			scope = "Namespaced"
		}
		g := gvr.GVR()
		crd := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]any{"name": g.Resource + "." + g.Group},
			"spec": map[string]any{
				"group": g.Group,
				"scope": scope,
				"names": map[string]any{
					"plural":   g.Resource,
					"singular": strings.ToLower(kind),
					"kind":     kind,
					"listKind": kind + "List",
				},
				"versions": []any{map[string]any{"name": g.Version, "served": true, "storage": true}},
			},
		}}
		c.objects = append(c.objects, crd)
	}
}

// WithRBAC adds an access policy. A verb is allowed when all policies allow it.
func WithRBAC(p Policy) Option {
	return func(c *Cluster) { c.policies = append(c.policies, p) }
}

// DenyVerb denies the given verbs on all resources.
func DenyVerb(verbs ...string) Option {
	return WithRBAC(func(_ string, _ *client.GVR, _, verb string) bool {
		for _, v := range verbs {
			if v == verb {
				return false
			}
		}
		return true
	})
}

// DenyNS denies all verbs in the given namespaces.
func DenyNS(nss ...string) Option {
	return WithRBAC(func(ns string, _ *client.GVR, _, _ string) bool {
		for _, n := range nss {
			if n == ns {
				return false
			}
		}
		return true
	})
}

// FailOn makes the nth matching call fail with err. Set nth to 0 to fail all
// matching calls. Use "*" to match any verb or resource. Calls on the typed
// and dynamic clients share one counter.
func FailOn(verb, resource string, nth int, err error) Option {
	return func(c *Cluster) {
		c.failures = append(c.failures, &failure{verb: verb, resource: resource, nth: nth, err: err})
	}
}

// MirrorTypedToDyn copies typed client mutations to the dynamic store, so
// informers see them.
func MirrorTypedToDyn() Option {
	return func(c *Cluster) { c.mirror = true }
}

// New builds a fake cluster.
func New(t testing.TB, opts ...Option) *Cluster {
	t.Helper()
	c := Cluster{
		t:          t,
		resources:  builtins(),
		byGVK:      make(map[schema.GroupVersionKind]Resource),
		namespaces: []string{client.DefaultNamespace},
		contexts:   []string{DefaultContext},
		context:    DefaultContext,
	}
	for _, o := range opts {
		o(&c)
	}
	c.connected.Store(true)
	for _, r := range c.resources {
		if _, ok := c.byGVK[r.gvk()]; !ok {
			c.byGVK[r.gvk()] = r
		}
	}
	c.cfg = client.NewConfig(c.flags())
	c.build()
	c.seed()
	c.conn = &Conn{c: &c}

	return &c
}

func (c *Cluster) flags() *genericclioptions.ConfigFlags {
	path := filepath.Join(c.t.TempDir(), "kubeconfig")
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Config\n")
	b.WriteString("clusters:\n- name: fake\n  cluster:\n    server: https://127.0.0.1:1\n")
	b.WriteString("users:\n- name: fake\n  user:\n    token: fake\n")
	b.WriteString("contexts:\n")
	for _, ct := range c.contexts {
		fmt.Fprintf(&b, "- name: %s\n  context:\n    cluster: fake\n    user: fake\n", ct)
	}
	fmt.Fprintf(&b, "current-context: %s\n", c.context)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		c.t.Fatalf("write kubeconfig: %v", err)
	}
	timeout, ct, cl := "5s", c.context, "fake"
	f := genericclioptions.NewConfigFlags(false)
	f.KubeConfig, f.Context, f.ClusterName, f.Timeout = &path, &ct, &cl, &timeout

	return f
}

func (c *Cluster) build() {
	c.typed = fake.NewClientset()
	disc, ok := c.typed.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		c.t.Fatalf("unexpected discovery type %T", c.typed.Discovery())
	}
	disc.Resources = discoveryLists(c.resources)
	disc.FakedServerVersion = &version.Info{Major: "1", Minor: "37", GitVersion: "v1.37.2"}
	c.disc = &cachedDiscovery{FakeDiscovery: disc}

	listKinds := make(map[schema.GroupVersionResource]string, len(c.resources))
	for _, r := range c.resources {
		listKinds[r.gvr()] = r.Kind + "List"
	}
	c.dyn = dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds)
	c.mx = mxfake.NewSimpleClientset()

	// Prepend order matters: the last one runs first.
	if c.mirror {
		c.typed.PrependReactor("*", "*", c.mirrorReaction)
	}
	for _, f := range c.failures {
		c.typed.PrependReactor("*", "*", f.reaction)
		c.dyn.PrependReactor("*", "*", f.reaction)
	}
	c.typed.PrependReactor("*", "*", c.record(SourceTyped))
	c.dyn.PrependReactor("*", "*", c.record(SourceDyn))
}

func (c *Cluster) seed() {
	nss := make(map[string]struct{})
	for _, ns := range c.namespaces {
		nss[ns] = struct{}{}
	}
	for _, o := range c.objects {
		if m, err := metaAccessor(o); err == nil && m.GetNamespace() != "" {
			nss[m.GetNamespace()] = struct{}{}
		}
	}
	for ns := range nss {
		c.Add(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for _, o := range c.objects {
		c.Add(o)
	}
	for _, o := range c.metrics {
		c.addMetrics(o)
	}
}

// Add stores an object after New. It fails the test on error.
func (c *Cluster) Add(o runtime.Object) {
	c.t.Helper()
	if err := c.add(o); err != nil {
		c.t.Fatalf("add object: %v", err)
	}
}

func (c *Cluster) add(o runtime.Object) error {
	var (
		u     *unstructured.Unstructured
		typed runtime.Object
	)
	if uo, ok := o.(*unstructured.Unstructured); ok {
		u = uo.DeepCopy()
		if t, err := scheme.Scheme.New(u.GroupVersionKind()); err == nil {
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, t); err == nil {
				typed = t
			}
		}
	} else {
		gvks, _, err := scheme.Scheme.ObjectKinds(o)
		if err != nil || len(gvks) == 0 {
			return fmt.Errorf("unknown kind %T: %w", o, err)
		}
		typed = o.DeepCopyObject()
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
		if err != nil {
			return err
		}
		u = &unstructured.Unstructured{Object: m}
		u.SetGroupVersionKind(gvks[0])
	}
	r, ok := c.byGVK[u.GroupVersionKind()]
	if !ok {
		return fmt.Errorf("no resource served for %s", u.GroupVersionKind())
	}
	if err := c.dyn.Tracker().Create(r.gvr(), u, u.GetNamespace()); err != nil {
		return err
	}
	if typed == nil {
		return nil
	}
	// Helm adds the CRD kinds to the client-go scheme, but the typed fake
	// cannot store them. Keep such objects in the dynamic store only.
	if err := c.typed.Tracker().Create(r.gvr(), typed, u.GetNamespace()); err != nil &&
		!apierrors.IsAlreadyExists(err) && !meta.IsNoMatchError(err) {
		return err
	}

	return nil
}

func (c *Cluster) addMetrics(o runtime.Object) {
	c.t.Helper()
	var (
		gvr schema.GroupVersionResource
		ns  string
	)
	switch m := o.(type) {
	case *mxv1beta1.PodMetrics:
		gvr, ns = client.PmxGVR.GVR(), m.Namespace
	case *mxv1beta1.NodeMetrics:
		gvr = client.NmxGVR.GVR()
	default:
		c.t.Fatalf("unsupported metrics object %T", o)
	}
	if err := c.mx.Tracker().Create(gvr, o, ns); err != nil {
		c.t.Fatalf("add metrics: %v", err)
	}
}

// Conn returns the connection to the fake cluster.
func (c *Cluster) Conn() client.Connection { return c.conn }

// Typed returns the typed fake clientset.
func (c *Cluster) Typed() *fake.Clientset { return c.typed }

// Dyn returns the dynamic fake client.
func (c *Cluster) Dyn() *dynfake.FakeDynamicClient { return c.dyn }

// Metrics returns the metrics fake clientset.
func (c *Cluster) Metrics() *mxfake.Clientset { return c.mx }

// SetConnected sets the result of ConnectionOK and CheckConnectivity.
func (c *Cluster) SetConnected(b bool) { c.connected.Store(b) }

// Invalidations returns how many times the discovery cache was invalidated.
func (c *Cluster) Invalidations() int { return int(c.disc.invalidations.Load()) }

// DynGet returns an object from the dynamic store.
func (c *Cluster) DynGet(gvr *client.GVR, ns, name string) (*unstructured.Unstructured, error) {
	o, err := c.dyn.Tracker().Get(gvr.GVR(), ns, name)
	if err != nil {
		return nil, err
	}
	u, ok := o.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", o)
	}

	return u, nil
}

func (c *Cluster) allowed(ns string, gvr *client.GVR, name, verb string) bool {
	for _, p := range c.policies {
		if !p(ns, gvr, name, verb) {
			return false
		}
	}

	return true
}

func (c *Cluster) mirrorReaction(a k8stesting.Action) (bool, runtime.Object, error) {
	verb := a.GetVerb()
	if a.GetSubresource() != "" || (verb != "create" && verb != "update" && verb != "patch" && verb != "delete") {
		return false, nil, nil
	}
	handled, obj, err := k8stesting.ObjectReaction(c.typed.Tracker())(a)
	if err != nil || !handled {
		return handled, obj, err
	}
	gvr, ns := a.GetResource(), a.GetNamespace()
	if verb == "delete" {
		if da, ok := a.(k8stesting.DeleteAction); ok {
			_ = c.dyn.Tracker().Delete(gvr, ns, da.GetName())
		}
		return handled, obj, err
	}
	gvks, _, kerr := scheme.Scheme.ObjectKinds(obj)
	m, cerr := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if kerr != nil || cerr != nil || len(gvks) == 0 {
		return handled, obj, err
	}
	u := &unstructured.Unstructured{Object: m}
	u.SetGroupVersionKind(gvks[0])
	if _, gerr := c.dyn.Tracker().Get(gvr, ns, u.GetName()); gerr == nil {
		_ = c.dyn.Tracker().Update(gvr, u, ns)
	} else {
		_ = c.dyn.Tracker().Create(gvr, u, ns)
	}

	return handled, obj, err
}

func metaAccessor(o runtime.Object) (metav1.Object, error) {
	m, ok := o.(metav1.Object)
	if !ok {
		return nil, fmt.Errorf("no metadata on %T", o)
	}
	return m, nil
}

type failure struct {
	verb, resource string
	nth            int
	err            error

	mx    sync.Mutex
	count int
}

func (f *failure) reaction(a k8stesting.Action) (bool, runtime.Object, error) {
	if (f.verb != "*" && f.verb != a.GetVerb()) || (f.resource != "*" && f.resource != a.GetResource().Resource) {
		return false, nil, nil
	}
	f.mx.Lock()
	f.count++
	n := f.count
	f.mx.Unlock()
	if f.nth == 0 || f.nth == n {
		return true, nil, f.err
	}

	return false, nil, nil
}

type cachedDiscovery struct {
	*fakediscovery.FakeDiscovery

	invalidations atomic.Int64
}

func (*cachedDiscovery) Fresh() bool { return true }

func (d *cachedDiscovery) Invalidate() { d.invalidations.Add(1) }

func (d *cachedDiscovery) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	_, rr, err := d.ServerGroupsAndResources()
	if err != nil {
		return nil, err
	}
	out := make([]*metav1.APIResourceList, 0, len(rr))
	for _, l := range rr {
		nl := metav1.APIResourceList{GroupVersion: l.GroupVersion}
		for _, r := range l.APIResources {
			if !strings.Contains(r.Name, "/") {
				nl.APIResources = append(nl.APIResources, r)
			}
		}
		out = append(out, &nl)
	}

	return out, nil
}

func (d *cachedDiscovery) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	rr, err := d.ServerPreferredResources()
	if err != nil {
		return nil, err
	}
	out := make([]*metav1.APIResourceList, 0, len(rr))
	for _, l := range rr {
		nl := metav1.APIResourceList{GroupVersion: l.GroupVersion}
		for _, r := range l.APIResources {
			if r.Namespaced {
				nl.APIResources = append(nl.APIResources, r)
			}
		}
		out = append(out, &nl)
	}

	return out, nil
}
