// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package watch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/derailed/k9s/internal"
	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/slogs"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	di "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
)

const (
	defaultResync   = 10 * time.Minute
	defaultWaitTime = 500 * time.Millisecond

	// factoryIdleTTL is how long an unused namespace's informers are kept
	// before being stopped. Without it every namespace visited during the
	// day keeps its watches (and a full copy of its objects) open.
	factoryIdleTTL     = 10 * time.Minute
	factoryJanitorTick = time.Minute
)

// Factory tracks various resource informers.
type Factory struct {
	factories  map[string]di.DynamicSharedInformerFactory
	stops      map[string]chan struct{}
	lastUsed   map[string]time.Time
	cwInfs     map[*client.GVR]informers.GenericInformer
	activeNS   string
	client     client.Connection
	stopChan   chan struct{}
	forwarders Forwarders
	mx         sync.RWMutex
}

// NewFactory returns a new informers factory.
func NewFactory(clt client.Connection) *Factory {
	return &Factory{
		client:     clt,
		factories:  make(map[string]di.DynamicSharedInformerFactory),
		stops:      make(map[string]chan struct{}),
		lastUsed:   make(map[string]time.Time),
		cwInfs:     make(map[*client.GVR]informers.GenericInformer),
		forwarders: NewForwarders(),
	}
}

// Start initializes the informers until caller cancels the context.
func (f *Factory) Start(ns string) {
	f.mx.Lock()
	defer f.mx.Unlock()

	slog.Debug("Factory started", slogs.Namespace, ns)
	f.stopChan = make(chan struct{})
	for ns, fac := range f.factories {
		slog.Debug("Starting factory for ns", slogs.Namespace, ns)
		fac.Start(f.stopFor(ns))
	}
	go f.janitor(f.stopChan)
}

// Terminate terminates all watchers and forwards.
func (f *Factory) Terminate() {
	f.mx.Lock()
	defer f.mx.Unlock()

	if f.stopChan != nil {
		close(f.stopChan)
		f.stopChan = nil
	}
	for k := range f.factories {
		f.dropFactoryLocked(k)
	}
	f.forwarders.DeleteAll()
}

// stopFor returns the stop channel of a namespace's informers.
// Callers must hold f.mx.
func (f *Factory) stopFor(ns string) chan struct{} {
	c, ok := f.stops[ns]
	if !ok {
		c = make(chan struct{})
		f.stops[ns] = c
	}

	return c
}

// dropFactoryLocked stops and forgets a namespace's informers.
// Callers must hold f.mx.
func (f *Factory) dropFactoryLocked(ns string) {
	if c, ok := f.stops[ns]; ok {
		close(c)
	}
	delete(f.stops, ns)
	delete(f.factories, ns)
	delete(f.lastUsed, ns)
	if ns == client.BlankNamespace {
		clear(f.cwInfs)
	}
}

func (f *Factory) janitor(stop <-chan struct{}) {
	defer internal.LogPanic("factory janitor")
	t := time.NewTicker(factoryJanitorTick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			f.evictIdle(time.Now(), factoryIdleTTL)
		}
	}
}

// evictIdle stops informers of namespaces not used for longer than ttl.
// The active namespace is always kept.
func (f *Factory) evictIdle(now time.Time, ttl time.Duration) {
	f.mx.Lock()
	defer f.mx.Unlock()

	for ns := range f.factories {
		if ns == f.activeNS {
			continue
		}
		if now.Sub(f.lastUsed[ns]) < ttl {
			continue
		}
		slog.Debug("Stopping idle informers", slogs.Namespace, ns)
		f.dropFactoryLocked(ns)
	}
}

// List returns a resource collection.
func (f *Factory) List(gvr *client.GVR, ns string, wait bool, lbls labels.Selector) ([]runtime.Object, error) {
	if client.IsAllNamespace(ns) {
		ns = client.BlankNamespace
	}
	inf, err := f.CanForResource(ns, gvr, client.ListAccess)
	if err != nil {
		return nil, err
	}

	var oo []runtime.Object
	if client.IsClusterScoped(ns) {
		oo, err = inf.Lister().List(lbls)
	} else {
		oo, err = inf.Lister().ByNamespace(ns).List(lbls)
	}
	if !wait || (wait && inf.Informer().HasSynced()) {
		return oo, err
	}

	f.waitForCacheSync(ns)
	if client.IsClusterScoped(ns) {
		return inf.Lister().List(lbls)
	}
	return inf.Lister().ByNamespace(ns).List(lbls)
}

// HasSynced checks if given informer is up to date.
func (f *Factory) HasSynced(gvr *client.GVR, ns string) (bool, error) {
	inf, err := f.CanForResource(ns, gvr, client.ListAccess)
	if err != nil {
		return false, err
	}

	return inf.Informer().HasSynced(), nil
}

// Get retrieves a given resource.
func (f *Factory) Get(gvr *client.GVR, fqn string, wait bool, _ labels.Selector) (runtime.Object, error) {
	ns, n := namespaced(fqn)
	if client.IsAllNamespace(ns) {
		ns = client.BlankNamespace
	}

	inf, err := f.CanForInstance(fqn, gvr, []string{client.GetVerb})
	if err != nil {
		return nil, err
	}
	var o runtime.Object
	if client.IsClusterScoped(ns) {
		o, err = inf.Lister().Get(n)
	} else {
		o, err = inf.Lister().ByNamespace(ns).Get(n)
	}
	if !wait || (wait && inf.Informer().HasSynced()) {
		return o, err
	}

	f.waitForCacheSync(ns)
	if !inf.Informer().HasSynced() {
		// Cold cache on a large LIST would report not found, so ask the api server.
		dial, err := f.client.DynDial()
		if err != nil {
			return nil, err
		}
		if client.IsClusterScoped(ns) {
			return dial.Resource(gvr.GVR()).Get(context.Background(), n, metav1.GetOptions{})
		}
		return dial.Resource(gvr.GVR()).Namespace(ns).Get(context.Background(), n, metav1.GetOptions{})
	}
	if client.IsClusterScoped(ns) {
		return inf.Lister().Get(n)
	}

	return inf.Lister().ByNamespace(ns).Get(n)
}

func (f *Factory) waitForCacheSync(ns string) {
	if client.IsClusterWide(ns) {
		ns = client.BlankNamespace
	}

	f.mx.RLock()
	fac, ok := f.factories[ns]
	f.mx.RUnlock()
	if !ok {
		return
	}

	// Hang for a sec for the cache to refresh if still not done bail out!
	c := make(chan struct{})
	go func(c chan struct{}) {
		<-time.After(defaultWaitTime)
		close(c)
	}(c)
	_ = fac.WaitForCacheSync(c)
}

// WaitForCacheSync waits for all factories to update their cache.
func (f *Factory) WaitForCacheSync() {
	f.mx.RLock()
	stop := f.stopChan
	facs := make(map[string]di.DynamicSharedInformerFactory, len(f.factories))
	for ns, fac := range f.factories {
		facs[ns] = fac
	}
	f.mx.RUnlock()

	for ns, fac := range facs {
		m := fac.WaitForCacheSync(stop)
		for k, v := range m {
			slog.Debug("Cache loaded",
				slogs.Namespace, ns,
				slogs.ResGrpVersion, k,
				"synced", v,
			)
		}
	}
}

// Client return the factory connection.
func (f *Factory) Client() client.Connection {
	return f.client
}

// FactoryFor returns a factory for a given namespace.
func (f *Factory) FactoryFor(ns string) di.DynamicSharedInformerFactory {
	f.mx.RLock()
	defer f.mx.RUnlock()

	return f.factories[ns]
}

// SetActiveNS sets the active namespace.
func (f *Factory) SetActiveNS(ns string) error {
	if client.IsClusterWide(ns) {
		ns = client.BlankNamespace
	}
	f.mx.Lock()
	f.activeNS = ns
	f.mx.Unlock()
	if f.isClusterWide() {
		return nil
	}
	_, err := f.ensureFactory(ns)
	return err
}

func (f *Factory) isClusterWide() bool {
	f.mx.RLock()
	defer f.mx.RUnlock()
	_, ok := f.factories[client.BlankNamespace]

	return ok
}

// CanForResource return an informer is user has access.
func (f *Factory) CanForResource(ns string, gvr *client.GVR, verbs []string) (informers.GenericInformer, error) {
	var resName string
	if gvr == client.NsGVR {
		resName = ns
	}
	auth, err := f.Client().CanI(ns, gvr, resName, verbs)
	if err != nil {
		return nil, err
	}
	if !auth {
		return nil, fmt.Errorf("%v access denied on resource %q:%q", verbs, ns, gvr)
	}

	// Namespaces are cluster-scoped; always use cluster scope for the informer
	if gvr == client.NsGVR {
		ns = client.ClusterScope
	}

	return f.ForResource(ns, gvr)
}

// CanForInstance return an informer is user has access.
func (f *Factory) CanForInstance(fqn string, gvr *client.GVR, verbs []string) (informers.GenericInformer, error) {
	ns, n := namespaced(fqn)
	if client.IsAllNamespace(ns) {
		ns = client.BlankNamespace
	}

	// For namespace resources, use the resource name as the namespace for RBAC
	// (RoleBindings within that namespace can grant permissions),
	// but keep the original ns for the informer since namespaces are cluster-scoped.
	authNs := ns
	if gvr == client.NsGVR {
		authNs = n
	}

	auth, err := f.Client().CanI(authNs, gvr, n, verbs)
	if err != nil {
		return nil, err
	}
	if !auth {
		return nil, fmt.Errorf("%v access denied on resource %q:%q", verbs, authNs, gvr)
	}

	return f.ForResource(ns, gvr)
}

// ForResource returns an informer for a given resource.
func (f *Factory) ForResource(ns string, gvr *client.GVR) (informers.GenericInformer, error) {
	// A synced cluster-wide informer already holds every namespace's objects:
	// serve namespaced reads from it rather than caching them a second time.
	if !client.IsClusterWide(ns) {
		if inf, ok := f.clusterWideInformer(gvr); ok {
			return inf, nil
		}
	}

	fact, err := f.ensureFactory(ns)
	if err != nil {
		return nil, err
	}
	inf := fact.ForResource(gvr.GVR())
	if inf == nil {
		slog.Error("No informer found",
			slogs.GVR, gvr,
			slogs.Namespace, ns,
		)
		return inf, nil
	}
	// Must happen before the informer starts; later calls are no-ops.
	tx := stripManagedFields
	if gvr == client.CrdGVR {
		tx = stripCRDSchemas
	}
	_ = inf.Informer().SetTransform(tx)

	f.mx.Lock()
	defer f.mx.Unlock()
	if client.IsClusterWide(ns) {
		f.cwInfs[gvr] = inf
		ns = client.BlankNamespace
	}
	// The factory may have been evicted meanwhile; only start informers
	// that are still tracked so they can be stopped later.
	if cur, ok := f.factories[ns]; ok && cur == fact {
		fact.Start(f.stopFor(ns))
	}

	return inf, nil
}

func (f *Factory) clusterWideInformer(gvr *client.GVR) (informers.GenericInformer, bool) {
	f.mx.RLock()
	inf, ok := f.cwInfs[gvr]
	f.mx.RUnlock()
	if !ok || !inf.Informer().HasSynced() {
		return nil, false
	}
	f.mx.Lock()
	f.lastUsed[client.BlankNamespace] = time.Now()
	f.mx.Unlock()

	return inf, true
}

func (f *Factory) ensureFactory(ns string) (di.DynamicSharedInformerFactory, error) {
	if client.IsClusterWide(ns) {
		ns = client.BlankNamespace
	}
	f.mx.Lock()
	defer f.mx.Unlock()
	f.lastUsed[ns] = time.Now()
	if fac, ok := f.factories[ns]; ok {
		return fac, nil
	}

	dial, err := f.client.DynDial()
	if err != nil {
		return nil, err
	}
	f.factories[ns] = di.NewFilteredDynamicSharedInformerFactory(
		dial,
		defaultResync,
		ns,
		nil,
	)

	return f.factories[ns], nil
}

// AddForwarder registers a new portforward for a given container.
func (f *Factory) AddForwarder(pf Forwarder) {
	f.mx.Lock()
	defer f.mx.Unlock()

	f.forwarders[pf.ID()] = pf
}

// DeleteForwarder deletes portforward for a given container.
func (f *Factory) DeleteForwarder(path string) {
	f.mx.Lock()
	count := f.forwarders.Kill(path)
	f.mx.Unlock()

	slog.Warn("Deleted portforward",
		slogs.Count, count,
		slogs.GVR, path,
	)
}

// RemoveForwarder stops and removes the given forwarder only if it is still
// the registered one for its ID. This keeps a finished forward from tearing
// down a newer forward that reused the same ID.
func (f *Factory) RemoveForwarder(pf Forwarder) {
	f.mx.Lock()
	defer f.mx.Unlock()

	if cur, ok := f.forwarders[pf.ID()]; ok && cur == pf {
		delete(f.forwarders, pf.ID())
	}
	pf.Stop()
}

// Forwarders returns a snapshot of all portforwards.
// The returned map is a copy and is safe to iterate concurrently.
func (f *Factory) Forwarders() Forwarders {
	f.mx.RLock()
	defer f.mx.RUnlock()

	return f.forwarders.Clone()
}

// ForwarderFor returns a portforward for a given container or nil if none exists.
func (f *Factory) ForwarderFor(path string) (Forwarder, bool) {
	f.mx.RLock()
	defer f.mx.RUnlock()

	fwd, ok := f.forwarders[path]

	return fwd, ok
}

// ValidatePortForwards check if pods are still around for portforwards.
func (f *Factory) ValidatePortForwards() {
	stale := make(map[string]Forwarder)
	for k, fwd := range f.Forwarders() {
		tokens := strings.Split(k, ":")
		if len(tokens) != 2 {
			slog.Error("Invalid port-forward key", slogs.Key, k)
			continue
		}
		paths := strings.Split(tokens[0], "|")
		// wait=true falls back to the api server when the cache is cold so a
		// not-found answer is authoritative.
		o, err := f.Get(client.PodGVR, paths[0], true, labels.Everything())
		if err != nil {
			// Only reap the forward if the pod is truly gone. Transient errors
			// (cold cache, RBAC hiccups, network) must not kill active forwards.
			if apierrors.IsNotFound(err) {
				stale[k] = fwd
			}
			continue
		}
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		var pod v1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &pod); err != nil {
			continue
		}
		if pod.GetCreationTimestamp().Unix() > fwd.Age().Unix() {
			stale[k] = fwd
		}
	}
	if len(stale) == 0 {
		return
	}

	f.mx.Lock()
	defer f.mx.Unlock()
	for k, fwd := range stale {
		// Skip if the forward was replaced while we were validating.
		if cur, ok := f.forwarders[k]; !ok || cur != fwd {
			continue
		}
		fwd.Stop()
		delete(f.forwarders, k)
	}
}

// stripManagedFields drops metadata.managedFields from cached objects. They
// are often the largest part of an object (30-50%), k9s never reads them from
// the cache, and views that show them fetch the live object instead. A nil
// managedFields on a later update means "unchanged" to the API server.
func stripManagedFields(o any) (any, error) {
	if u, ok := o.(*unstructured.Unstructured); ok && u.Object != nil {
		if meta, ok := u.Object["metadata"].(map[string]any); ok {
			delete(meta, "managedFields")
		}
	}

	return o, nil
}

// stripCRDSchemas also drops the OpenAPI schemas of cached CRDs: they are by
// far the largest objects on most clusters (hundreds of MB with some
// operators) and k9s never reads them from the cache.
func stripCRDSchemas(o any) (any, error) {
	o, _ = stripManagedFields(o)
	u, ok := o.(*unstructured.Unstructured)
	if !ok || u.Object == nil {
		return o, nil
	}
	if spec, ok := u.Object["spec"].(map[string]any); ok {
		if vv, ok := spec["versions"].([]any); ok {
			for _, v := range vv {
				if vm, ok := v.(map[string]any); ok {
					delete(vm, "schema")
				}
			}
		}
	}

	return o, nil
}
