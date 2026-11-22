// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/port"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/kubectl/pkg/drain"
)

// badObj returns an object with a wrong value type at path.
func badObj(gvr *client.GVR, kind, ns, name string, v any, path ...string) *unstructured.Unstructured {
	u := mutObj(gvr, kind, ns, name)
	u.Object[path[0]] = nestFields(v, path[1:]...)

	return u
}

func nestFields(v any, path ...string) any {
	if len(path) == 0 {
		return v
	}
	return map[string]any{path[0]: nestFields(v, path[1:]...)}
}

func TestRestartMalformedObject(t *testing.T) {
	kinds := map[string]string{"dp": "Deployment", "ds": "DaemonSet", "sts": "StatefulSet"}
	for fk, fam := range restartFamilies {
		t.Run(fk, func(t *testing.T) {
			o := badObj(fam.gvr, kinds[fk], "ns1", "a", "oops", "spec", "template", "spec", "containers")
			fc := fakecluster.New(t, fakecluster.WithObjects(o))
			r := fam.new()
			r.Init(mutFactory(t, fc.Conn()), fam.gvr)

			err := r.Restart(context.Background(), "ns1/a", &metav1.PatchOptions{})

			require.ErrorContains(t, err, "cannot restore slice from string")
			assert.Empty(t, fc.Actions("patch", "*"))
		})
	}
}

func TestRestartMissingMergeKey(t *testing.T) {
	for fk, fam := range restartFamilies {
		t.Run(fk, func(t *testing.T) {
			oo := mutWorkloads()
			for _, o := range oo {
				switch w := o.(type) {
				case *appsv1.Deployment:
					w.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{}}
				case *appsv1.DaemonSet:
					w.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{}}
				case *appsv1.StatefulSet:
					w.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{}}
				}
			}
			fc := fakecluster.New(t, fakecluster.WithObjects(oo...))
			r := fam.new()
			r.Init(mutFactory(t, fc.Conn()), fam.gvr)

			err := r.Restart(context.Background(), "ns1/a", &metav1.PatchOptions{})

			require.ErrorContains(t, err, "does not contain declared merge key: name")
			assert.Empty(t, fc.Actions("patch", "*"))
		})
	}
}

func TestRestartEncodeFailure(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutWorkloads()...))
	f := mutFactory(t, fc.Conn())

	err := restartRes[*corev1.Pod](context.Background(), f, client.DpGVR, "ns1/a", &metav1.PatchOptions{})

	require.ErrorContains(t, err, "not suitable for converting")
	assert.Empty(t, fc.Actions("patch", "*"))
}

func TestRestartInvalidPaths(t *testing.T) {
	for fk, fam := range restartFamilies {
		for _, path := range []string{"", "/", "ns1/", "ns1/a/b", "nope/a"} {
			t.Run(fk+"/"+path, func(t *testing.T) {
				fc := fakecluster.New(t, fakecluster.WithObjects(mutWorkloads()...))
				r := fam.new()
				r.Init(mutFactory(t, fc.Conn()), fam.gvr)

				require.Error(t, r.Restart(context.Background(), path, &metav1.PatchOptions{}))
				assert.Empty(t, fc.Actions("patch", "*"))
			})
		}
	}
}

func TestCronJobRunMalformed(t *testing.T) {
	uu := map[string]*unstructured.Unstructured{
		"spec":     badObj(client.CjGVR, "CronJob", "ns1", "cj1", "oops", "spec"),
		"template": badObj(client.CjGVR, "CronJob", "ns1", "cj1", []any{"x"}, "spec", "jobTemplate"),
		"suspend":  badObj(client.CjGVR, "CronJob", "ns1", "cj1", "yes", "spec", "suspend"),
	}
	for k, o := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, fakecluster.WithObjects(o))

			require.EqualError(t, newCronJob(t, fc.Conn()).Run("ns1/cj1"), "expecting CronJob resource")
			assert.Empty(t, fc.Actions("create", "*"))
		})
	}
}

func TestCronJobRunAPIErrors(t *testing.T) {
	gr := schema.GroupResource{Group: "batch", Resource: "jobs"}
	uu := map[string]struct {
		err   error
		check func(error) bool
	}{
		"conflict":  {err: apierrors.NewConflict(gr, "x", errMut), check: apierrors.IsConflict},
		"forbidden": {err: apierrors.NewForbidden(gr, "x", errMut), check: apierrors.IsForbidden},
		"exists":    {err: apierrors.NewAlreadyExists(gr, "x"), check: apierrors.IsAlreadyExists},
		"timeout":   {err: context.DeadlineExceeded, check: func(e error) bool { return errors.Is(e, context.DeadlineExceeded) }},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t,
				fakecluster.WithObjects(mutCronJob("cj1", nil)),
				fakecluster.FailOn("create", "jobs", 0, u.err),
			)

			err := newCronJob(t, fc.Conn()).Run("ns1/cj1")

			require.Error(t, err)
			assert.True(t, u.check(err), err)
			assert.Len(t, fc.Actions("create", "jobs"), 1)
		})
	}
}

// useMarshal swaps the patch marshaler for the test duration.
func useMarshal(t *testing.T, err error) {
	t.Helper()
	old := jsonMarshal
	jsonMarshal = func(any) ([]byte, error) { return nil, err }
	t.Cleanup(func() { jsonMarshal = old })
}

func TestSetImagesMarshalFailure(t *testing.T) {
	for fk, fam := range imageFamilies {
		t.Run(fk, func(t *testing.T) {
			useMarshal(t, errMut)
			fc := fakecluster.New(t, fakecluster.WithObjects(mutImageObjects(false)...))
			s := fam.new()
			s.Init(mutFactory(t, fc.Conn()), fam.gvr)

			err := s.SetImages(context.Background(), "ns1/a", ImageSpecs{{Name: "c1", DockerImage: "x"}})

			require.ErrorIs(t, err, errMut)
			assert.Empty(t, fc.Actions("patch", "*"))
		})
	}
}

func TestSetImagesInvalidInput(t *testing.T) {
	uu := map[string]struct {
		path  string
		specs ImageSpecs
	}{
		"missing":   {path: "ns1/nope", specs: ImageSpecs{{Name: "c1", DockerImage: "x"}}},
		"empty":     {path: "", specs: ImageSpecs{{Name: "c1", DockerImage: "x"}}},
		"bad-ns":    {path: "ns9/a", specs: ImageSpecs{{Name: "c1", DockerImage: "x"}}},
		"no-specs":  {path: "ns1/nope", specs: nil},
		"too-deep":  {path: "ns1/a/b", specs: ImageSpecs{{Name: "c1", DockerImage: "x"}}},
		"no-name":   {path: "ns1/nope", specs: ImageSpecs{{DockerImage: "x"}}},
		"no-image":  {path: "ns1/nope", specs: ImageSpecs{{Name: "c1"}}},
		"slash-ns":  {path: "/a", specs: ImageSpecs{{Name: "c1", DockerImage: "x"}}},
		"trailing":  {path: "ns1/", specs: ImageSpecs{{Name: "c1", DockerImage: "x"}}},
		"only-init": {path: "ns1/nope", specs: ImageSpecs{{Name: "i1", DockerImage: "x", Init: true}}},
	}
	for fk, fam := range imageFamilies {
		for k, u := range uu {
			t.Run(fk+"/"+k, func(t *testing.T) {
				fc := fakecluster.New(t, fakecluster.WithObjects(mutImageObjects(false)...))
				s := fam.new()
				s.Init(mutFactory(t, fc.Conn()), fam.gvr)

				err := s.SetImages(context.Background(), u.path, u.specs)

				require.Error(t, err)
				assert.NotContains(t, patchedTargets(fc), "ns1/a")
			})
		}
	}
}

// patchedTargets returns the ns/name of every recorded patch.
func patchedTargets(fc *fakecluster.Cluster) []string {
	var nn []string
	for _, a := range fc.Actions("patch", "*") {
		p := a.(k8stesting.PatchAction)
		nn = append(nn, client.FQN(p.GetNamespace(), p.GetName()))
	}

	return nn
}

func TestPodSetImagesMalformedPod(t *testing.T) {
	o := badObj(client.PodGVR, "Pod", "ns1", "a", "oops", "spec", "containers")
	fc := fakecluster.New(t, fakecluster.WithObjects(o))
	var p Pod
	p.Init(mutFactory(t, fc.Conn()), client.PodGVR)

	err := p.SetImages(context.Background(), "ns1/a", ImageSpecs{{Name: "c1", DockerImage: "x"}})

	require.ErrorContains(t, err, "cannot restore slice from string")
	assert.Empty(t, fc.Actions("patch", "*"))
}

func TestNodeToggleCordonBadGVR(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(mutNode("n1", false)))
	var n Node
	n.Init(mutFactory(t, fc.Conn()), client.NewGVR("bogus.io/v9/nodes"))

	require.ErrorContains(t, n.ToggleCordon("n1", true), "bogus.io/v9")
	assert.Empty(t, fc.Actions("patch", "*"))
	assert.Empty(t, fc.Actions("update", "*"))
}

func TestNodeToggleCordonPatchBuildFailure(t *testing.T) {
	old := cordonPatch
	t.Cleanup(func() { cordonPatch = old })
	var calls int
	cordonPatch = func(*drain.CordonHelper, kubernetes.Interface, bool) (error, error) {
		calls++
		return nil, errMut
	}
	fc := fakecluster.New(t, fakecluster.WithObjects(mutNode("n1", false)))

	require.ErrorIs(t, newNode(t, fc.Conn()).ToggleCordon("n1", true), errMut)
	assert.Equal(t, 1, calls)
	assert.Empty(t, fc.Actions("patch", "*"))
}

func TestNodeToggleCordonMalformedNode(t *testing.T) {
	o := badObj(client.NodeGVR, "Node", "", "n1", "yes", "spec", "unschedulable")
	fc := fakecluster.New(t, fakecluster.WithObjects(o))

	require.ErrorContains(t, newNode(t, fc.Conn()).ToggleCordon("n1", true), "unrecognized type: bool")
	assert.Empty(t, fc.Actions("patch", "*"))
}

func TestNodeToggleCordonInvalidPaths(t *testing.T) {
	for _, path := range []string{"", "/", "n9", "a/b/c"} {
		t.Run(path, func(t *testing.T) {
			fc := fakecluster.New(t, fakecluster.WithObjects(mutNode("n1", false)))

			require.Error(t, newNode(t, fc.Conn()).ToggleCordon(path, true))
			assert.Empty(t, fc.Actions("patch", "*"))
		})
	}
}

// listFactory serves a fixed list and uses the wrapped factory for all else.
type listFactory struct {
	Factory
	oo []runtime.Object
}

func (f *listFactory) List(*client.GVR, string, bool, labels.Selector) ([]runtime.Object, error) {
	return f.oo, nil
}

func TestPodSanitizeSkipsBadItems(t *testing.T) {
	good := phasePod("p1", corev1.PodPending)
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(good)
	require.NoError(t, err)
	fc := fakecluster.New(t, fakecluster.WithObjects(good, phasePod("p2", corev1.PodPending)))
	oo := []runtime.Object{
		phasePod("p2", corev1.PodPending),
		badObj(client.PodGVR, "Pod", "ns1", "p3", "oops", "status"),
		&unstructured.Unstructured{Object: m},
	}
	var p Pod
	p.Init(&listFactory{Factory: mutFactory(t, fc.Conn()), oo: oo}, client.PodGVR)

	n, err := p.Sanitize(context.Background(), "ns1")

	require.NoError(t, err)
	assert.Equal(t, 1, n)
	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, "p1", dd[0].Name)
}

func TestPodSanitizeMalformedStore(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(
		badObj(client.PodGVR, "Pod", "ns1", "p3", int64(5), "status", "phase"),
		phasePod("p1", corev1.PodPending),
	))

	n, err := sanitizePod(t, fc).Sanitize(context.Background(), "ns1")

	require.NoError(t, err)
	assert.Equal(t, 1, n)
	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, "p1", dd[0].Name)
}

func TestFluxPatchAPIErrors(t *testing.T) {
	gr := schema.GroupResource{Group: "kustomize.toolkit.fluxcd.io", Resource: "kustomizations"}
	uu := map[string]struct {
		err   error
		check func(error) bool
	}{
		"not-found": {err: apierrors.NewNotFound(gr, "web"), check: apierrors.IsNotFound},
		"conflict":  {err: apierrors.NewConflict(gr, "web", errMut), check: apierrors.IsConflict},
		"forbidden": {err: apierrors.NewForbidden(gr, "web", errMut), check: apierrors.IsForbidden},
		"invalid":   {err: apierrors.NewBadRequest("strict decoding error: unknown field"), check: apierrors.IsBadRequest},
		"timeout":   {err: context.DeadlineExceeded, check: func(e error) bool { return errors.Is(e, context.DeadlineExceeded) }},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			ks := obj("apps", "web", map[string]any{})
			ks.SetAPIVersion("kustomize.toolkit.fluxcd.io/v1")
			ks.SetKind("Kustomization")
			dial := fake.NewSimpleDynamicClientWithCustomListKinds(
				runtime.NewScheme(),
				map[schema.GroupVersionResource]string{ksGVR.GVR(): "KustomizationList"},
				ks,
			)
			dial.PrependReactor("patch", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, u.err
			})

			err := FluxPatch(context.Background(), dial, FluxTarget{GVR: ksGVR, Namespace: "apps", Name: "web"}, FluxSuspendPatch(FluxCaps{}, true, "now"))

			require.ErrorContains(t, err, "kustomizations apps/web: ")
			assert.True(t, u.check(err), err)
			o, gerr := dial.Resource(ksGVR.GVR()).Namespace("apps").Get(context.Background(), "web", metav1.GetOptions{})
			require.NoError(t, gerr)
			assert.False(t, FluxIsSuspended(o, FluxCaps{}))
		})
	}
}

func TestFluxPatchMalformedBody(t *testing.T) {
	ks := obj("apps", "web", map[string]any{})
	ks.SetAPIVersion("kustomize.toolkit.fluxcd.io/v1")
	ks.SetKind("Kustomization")
	dial := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{ksGVR.GVR(): "KustomizationList"},
		ks,
	)

	err := FluxPatch(context.Background(), dial, FluxTarget{GVR: ksGVR, Namespace: "apps", Name: "web"}, []byte("{not json"))

	require.ErrorContains(t, err, "kustomizations apps/web: ")
	o, gerr := dial.Resource(ksGVR.GVR()).Namespace("apps").Get(context.Background(), "web", metav1.GetOptions{})
	require.NoError(t, gerr)
	assert.Equal(t, map[string]any{}, o.Object["spec"])
}

func TestFluxPatchMissingTarget(t *testing.T) {
	dial := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{ksGVR.GVR(): "KustomizationList"},
	)

	err := FluxPatch(context.Background(), dial, FluxTarget{GVR: ksGVR, Namespace: "apps", Name: "web"}, []byte(`{}`))

	require.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err), err)
}

func TestFluxIsSuspendedInvalid(t *testing.T) {
	assert.False(t, FluxIsSuspended(nil, FluxCaps{}))
	assert.False(t, FluxIsSuspended(nil, FluxCaps{Operator: true}))
	assert.False(t, FluxIsSuspended(obj("ns", "a", map[string]any{"suspend": "true"}), FluxCaps{}))
	assert.False(t, FluxIsSuspended(&unstructured.Unstructured{Object: map[string]any{"spec": "x"}}, FluxCaps{}))
}

const pfAuthPlugin = "k9s-invalid-test"

var (
	pfAuthOnce   sync.Once
	pfAuthCalls  atomic.Int32
	pfAuthFailAt atomic.Int32
)

type pfAuth struct{}

func (pfAuth) WrapTransport(rt http.RoundTripper) http.RoundTripper { return rt }
func (pfAuth) Login() error                                         { return nil }

// usePFAuth makes the nth auth provider build fail. Set n to 0 to never fail.
func usePFAuth(t *testing.T, n int32) {
	t.Helper()
	pfAuthOnce.Do(func() {
		require.NoError(t, restclient.RegisterAuthProviderPlugin(pfAuthPlugin,
			func(string, map[string]string, restclient.AuthProviderConfigPersister) (restclient.AuthProvider, error) {
				if pfAuthCalls.Add(1) == pfAuthFailAt.Load() {
					return nil, errMut
				}
				return pfAuth{}, nil
			}))
	})
	pfAuthCalls.Store(0)
	pfAuthFailAt.Store(n)
}

// pfAuthConfig returns a client config whose user has the test auth provider.
func pfAuthConfig(t *testing.T) *client.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	kc := `apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: https://127.0.0.1:1
users:
- name: fake
  user:
    auth-provider:
      name: ` + pfAuthPlugin + `
contexts:
- name: fake
  context:
    cluster: fake
    user: fake
current-context: fake
`
	require.NoError(t, os.WriteFile(path, []byte(kc), 0o600))
	f := genericclioptions.NewConfigFlags(false)
	f.KubeConfig = &path

	return client.NewConfig(f)
}

// pfMutConn overrides config access and fails CanI for one gvr and verb.
type pfMutConn struct {
	client.Connection

	cfg, rest      *client.Config
	canIGVR, canIV string
}

func (c *pfMutConn) Config() *client.Config {
	if c.cfg == nil {
		return c.Connection.Config()
	}
	return c.cfg
}

func (c *pfMutConn) RestConfig() (*restclient.Config, error) {
	if c.rest == nil {
		return c.Connection.RestConfig()
	}
	return c.rest.RESTConfig()
}

func (c *pfMutConn) CanI(ns string, gvr *client.GVR, n string, verbs []string) (bool, error) {
	if gvr.String() == c.canIGVR && slices.Contains(verbs, c.canIV) {
		return false, errMut
	}
	return c.Connection.CanI(ns, gvr, n, verbs)
}

func pfObjects() []runtime.Object {
	return []runtime.Object{
		phasePod("p1", corev1.PodRunning),
		phasePod("p2", corev1.PodPending),
		badObj(client.PodGVR, "Pod", "ns1", "p3", "oops", "status"),
	}
}

func TestPortForwarderStartValid(t *testing.T) {
	for _, path := range []string{"ns1/p1", "ns1/p1|c1"} {
		t.Run(path, func(t *testing.T) {
			fc := fakecluster.New(t, fakecluster.WithObjects(pfObjects()...))
			pf := NewPortForwarder(mutFactory(t, fc.Conn()))

			fw, err := pf.Start(path, port.NewPortTunnel("127.0.0.1", "c1", "8080", "80"))

			require.NoError(t, err)
			require.NotNil(t, fw)
			assert.Equal(t, path, pf.path)
			assert.Empty(t, fc.Actions("create", "*"))
		})
	}
}

func TestPortForwarderStartSPDYOnly(t *testing.T) {
	t.Setenv("KUBECTL_PORT_FORWARD_WEBSOCKETS", "false")
	fc := fakecluster.New(t, fakecluster.WithObjects(pfObjects()...))
	pf := NewPortForwarder(mutFactory(t, fc.Conn()))

	fw, err := pf.Start("ns1/p1", port.NewPortTunnel("localhost", "c1", "8080", "80"))

	require.NoError(t, err)
	require.NotNil(t, fw)
}

func TestPortForwarderStartInvalid(t *testing.T) {
	uu := map[string]struct {
		path   string
		tunnel port.PortTunnel
		canI   [2]string
		err    string
	}{
		"cani-pod":    {path: "ns1/p1", canI: [2]string{"v1/pods", "get"}, err: "boom"},
		"cani-get":    {path: "ns1/p1", canI: [2]string{"v1/pods:portforward", "get"}, err: "boom"},
		"cani-create": {path: "ns1/p1", canI: [2]string{"v1/pods:portforward", "create"}, err: "boom"},
		"missing":     {path: "ns1/nope", err: "not found"},
		"empty":       {path: "", err: "not found"},
		"no-name":     {path: "ns1/", err: "not found"},
		"pipe-only":   {path: "ns1/|c1", err: "not found"},
		"malformed":   {path: "ns1/p3", err: "cannot restore struct from: string"},
		"pending":     {path: "ns1/p2", err: "pod is not running"},
		"bad-address": {path: "ns1/p1", tunnel: port.NewPortTunnel("not-an-ip", "c1", "8080", "80"), err: "not a valid IP"},
		"bad-port":    {path: "ns1/p1", tunnel: port.NewPortTunnel("localhost", "c1", "abc", "80"), err: "abc"},
		"neg-port":    {path: "ns1/p1", tunnel: port.NewPortTunnel("localhost", "c1", "-1", "80"), err: "-1"},
		"no-port":     {path: "ns1/p1", tunnel: port.NewPortTunnel("localhost", "c1", "", ""), err: "port"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, fakecluster.WithObjects(pfObjects()...))
			conn := &pfMutConn{Connection: fc.Conn(), canIGVR: u.canI[0], canIV: u.canI[1]}
			tt := u.tunnel
			if tt.Address == "" {
				tt = port.NewPortTunnel("localhost", "c1", "8080", "80")
			}

			fw, err := NewPortForwarder(mutFactory(t, conn)).Start(u.path, tt)

			require.ErrorContains(t, err, u.err)
			assert.Nil(t, fw)
		})
	}
}

func TestPortForwarderStartBadRESTConfig(t *testing.T) {
	uu := map[string]struct {
		ws  string
		max int32
	}{
		"websocket": {max: 4},
		"spdy-only": {ws: "false", max: 3},
	}
	for k, u := range uu {
		for n := int32(1); n <= u.max; n++ {
			t.Run(k, func(t *testing.T) {
				if u.ws != "" {
					t.Setenv("KUBECTL_PORT_FORWARD_WEBSOCKETS", u.ws)
				}
				usePFAuth(t, n)
				fc := fakecluster.New(t, fakecluster.WithObjects(pfObjects()...))
				cfg := pfAuthConfig(t)
				conn := &pfMutConn{Connection: fc.Conn(), cfg: cfg, rest: cfg}

				fw, err := NewPortForwarder(mutFactory(t, conn)).Start("ns1/p1", port.NewPortTunnel("localhost", "c1", "8080", "80"))

				require.ErrorIs(t, err, errMut)
				assert.Nil(t, fw)
			})
		}
		t.Run(k+"/ok", func(t *testing.T) {
			if u.ws != "" {
				t.Setenv("KUBECTL_PORT_FORWARD_WEBSOCKETS", u.ws)
			}
			usePFAuth(t, 0)
			fc := fakecluster.New(t, fakecluster.WithObjects(pfObjects()...))
			cfg := pfAuthConfig(t)
			conn := &pfMutConn{Connection: fc.Conn(), cfg: cfg, rest: cfg}

			fw, err := NewPortForwarder(mutFactory(t, conn)).Start("ns1/p1", port.NewPortTunnel("localhost", "c1", "8080", "80"))

			require.NoError(t, err)
			assert.NotNil(t, fw)
			assert.Equal(t, u.max, pfAuthCalls.Load())
		})
	}
}

func TestPortForwarderStartBadKubeconfig(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(pfObjects()...))
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte("{not yaml"), 0o600))
	f := genericclioptions.NewConfigFlags(false)
	f.KubeConfig = &path
	conn := &pfMutConn{Connection: fc.Conn(), cfg: client.NewConfig(f)}

	fw, err := NewPortForwarder(mutFactory(t, conn)).Start("ns1/p1", port.NewPortTunnel("localhost", "c1", "8080", "80"))

	require.Error(t, err)
	assert.Nil(t, fw)
}
