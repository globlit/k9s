// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package fakecluster

import (
	"fmt"
	"sync"

	"github.com/globlit/k9s/internal/client"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/metrics/pkg/client/clientset/versioned"
)

var _ client.Connection = (*Conn)(nil)

// Conn is a client.Connection backed by a fake cluster.
type Conn struct {
	c  *Cluster
	mx sync.Mutex
}

// CanI checks the RBAC policies and counts the call.
func (k *Conn) CanI(ns string, gvr *client.GVR, n string, verbs []string) (bool, error) {
	k.c.canICalls.Add(1)
	for _, v := range verbs {
		if !k.c.allowed(ns, gvr, n, v) {
			return false, nil
		}
	}

	return true, nil
}

// Config returns the client config built from the scratch kubeconfig.
func (k *Conn) Config() *client.Config { return k.c.cfg }

// ConnectionOK returns the connected state.
func (k *Conn) ConnectionOK() bool { return k.c.connected.Load() }

// CheckConnectivity returns the connected state.
func (k *Conn) CheckConnectivity() bool { return k.c.connected.Load() }

// Dial returns the typed fake.
func (k *Conn) Dial() (kubernetes.Interface, error) { return k.c.typed, nil }

// DialLogs returns the typed fake.
func (k *Conn) DialLogs() (kubernetes.Interface, error) { return k.c.typed, nil }

// SwitchContext switches to a context from the scratch kubeconfig.
func (k *Conn) SwitchContext(ctx string) error {
	k.mx.Lock()
	defer k.mx.Unlock()

	return k.c.cfg.SwitchContext(ctx)
}

// CachedDiscovery returns the fake discovery client.
func (k *Conn) CachedDiscovery() (discovery.CachedDiscoveryInterface, error) {
	return k.c.disc, nil
}

// RestConfig returns a config that points at a closed local port.
func (k *Conn) RestConfig() (*restclient.Config, error) { return k.c.cfg.RESTConfig() }

// MXDial returns the metrics fake.
func (k *Conn) MXDial() (versioned.Interface, error) { return k.c.mx, nil }

// DynDial returns the dynamic fake.
func (k *Conn) DynDial() (dynamic.Interface, error) { return k.c.dyn, nil }

// HasMetrics reports if WithMetrics was given.
func (k *Conn) HasMetrics() bool { return k.c.hasMetrics }

// ValidNamespaceNames returns the namespaces in the typed store.
func (k *Conn) ValidNamespaceNames() (client.NamespaceNames, error) {
	gvr := client.NsGVR.GVR()
	o, err := k.c.typed.Tracker().List(gvr, gvr.GroupVersion().WithKind("Namespace"), "")
	if err != nil {
		return nil, err
	}
	l, ok := o.(*corev1.NamespaceList)
	if !ok {
		return nil, fmt.Errorf("unexpected list type %T", o)
	}
	nn := make(client.NamespaceNames, len(l.Items))
	for i := range l.Items {
		nn[l.Items[i].Name] = struct{}{}
	}

	return nn, nil
}

// IsValidNamespace checks if ns exists or is cluster wide.
func (k *Conn) IsValidNamespace(ns string) bool {
	if client.IsClusterWide(ns) || ns == client.NotNamespaced {
		return true
	}
	nn, err := k.ValidNamespaceNames()
	if err != nil {
		return false
	}
	_, ok := nn[ns]

	return ok
}

// ServerVersion returns the fake server version.
func (k *Conn) ServerVersion() (*version.Info, error) { return k.c.disc.ServerVersion() }

// ActiveContext returns the current context name.
func (k *Conn) ActiveContext() string {
	k.mx.Lock()
	defer k.mx.Unlock()
	n, err := k.c.cfg.CurrentContextName()
	if err != nil {
		return ""
	}

	return n
}

// ActiveNamespace returns the namespace of the current context.
func (k *Conn) ActiveNamespace() string {
	k.mx.Lock()
	defer k.mx.Unlock()
	ns, err := k.c.cfg.CurrentNamespaceName()
	if err != nil {
		return client.BlankNamespace
	}

	return ns
}

// IsActiveNamespace checks if ns is the active namespace.
func (k *Conn) IsActiveNamespace(ns string) bool {
	a := k.ActiveNamespace()
	return a == client.BlankNamespace || a == ns
}
