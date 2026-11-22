// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package fakecluster_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
)

func apiCluster(t *testing.T, opts ...fakecluster.Option) (*fakecluster.Cluster, kubernetes.Interface, *rest.Config) {
	t.Helper()
	dp := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "dp1", Labels: map[string]string{"app": "a"}},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](2)},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	p := pod("ns1", "p1")
	p.Labels = map[string]string{"app": "a"}
	p.Spec.Containers = []corev1.Container{{Name: "c1"}, {Name: "c2"}}
	p.Status = corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}}
	opts = append([]fakecluster.Option{
		fakecluster.WithAPIServer(),
		fakecluster.WithObjects(p, pod("ns1", "p2"), dp),
	}, opts...)
	fc := fakecluster.New(t, opts...)
	cfg, err := fc.Conn().RestConfig()
	require.NoError(t, err)
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	return fc, cs, cfg
}

func tableClient(t *testing.T, cfg *rest.Config, gv schema.GroupVersion) *rest.RESTClient {
	t.Helper()
	c := rest.CopyConfig(cfg)
	c.GroupVersion = &gv
	c.APIPath = "/apis"
	if gv.Group == "" {
		c.APIPath = "/api"
	}
	c.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	rc, err := rest.RESTClientFor(c)
	require.NoError(t, err)

	return rc
}

func getTable(t *testing.T, rc *rest.RESTClient, include string, seg ...string) (*metav1.Table, error) {
	t.Helper()
	req := rc.Get().SetHeader("Accept", "application/json;as=Table;v=v1;g=meta.k8s.io").AbsPath(seg...)
	if include != "" {
		req = req.Param("includeObject", include)
	}
	var tbl metav1.Table
	err := req.Do(context.Background()).Into(&tbl)

	return &tbl, err
}

func TestAPIServerOff(t *testing.T) {
	cfg, err := fakecluster.New(t).Conn().RestConfig()
	require.NoError(t, err)
	assert.Equal(t, "https://127.0.0.1:1", cfg.Host)
}

func TestAPIServerDiscovery(t *testing.T) {
	_, _, cfg := apiCluster(t)
	dc, err := discovery.NewDiscoveryClientForConfig(cfg)
	require.NoError(t, err)

	v, err := dc.ServerVersion()
	require.NoError(t, err)
	assert.Equal(t, "v1.37.2", v.GitVersion)

	gg, rr, err := dc.ServerGroupsAndResources()
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, g := range gg {
		names[g.Name] = true
	}
	assert.True(t, names[""] && names["apps"] && names["events.k8s.io"], "groups: %v", names)
	var scale bool
	for _, l := range rr {
		for _, r := range l.APIResources {
			scale = scale || (l.GroupVersion == "apps/v1" && r.Name == "deployments/scale")
		}
	}
	assert.True(t, scale)

	_, err = dc.ServerResourcesForGroupVersion("nope.io/v1")
	assert.True(t, apierrors.IsNotFound(err), "got %v", err)
}

func TestAPIServerTypedReads(t *testing.T) {
	fc, cs, _ := apiCluster(t)
	ctx := context.Background()

	pp, err := cs.CoreV1().Pods("ns1").List(ctx, metav1.ListOptions{LabelSelector: "app=a"})
	require.NoError(t, err)
	require.Len(t, pp.Items, 1)
	assert.Equal(t, "p1", pp.Items[0].Name)

	all, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all.Items, 2)

	ns, err := cs.CoreV1().Namespaces().Get(ctx, "ns1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "ns1", ns.Name)

	_, err = cs.CoreV1().Pods("ns1").Get(ctx, "nope", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "got %v", err)

	assert.NotEmpty(t, fc.Actions("list", "pods"))
	assert.Contains(t, fc.RESTCalls(), "GET /api/v1/namespaces/ns1/pods/nope")
	fc.Reset()
	assert.Empty(t, fc.RESTCalls())
}

func TestAPIServerTable(t *testing.T) {
	_, _, cfg := apiCluster(t, fakecluster.WithObjects(
		&eventsv1.Event{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "e1"},
			Type:       "Warning", Reason: "Boom", Note: "it broke",
			Regarding: corev1.ObjectReference{Kind: "Pod", Name: "p1"},
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Namespace: "ns1", Name: "e2"},
			Type:           "Normal",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "p2"},
		},
		&schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: "pc1"}, Value: 10},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "rs1", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour))},
			Spec:       appsv1.ReplicaSetSpec{Replicas: ptr.To[int32](3)},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ds1"},
			Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 2, NumberReady: 1},
		},
	))

	uu := map[string]struct {
		gv      schema.GroupVersion
		path    []string
		include string
		cols    []string
		cells   []any
		obj     string
	}{
		"pods-meta": {
			gv:    corev1.SchemeGroupVersion,
			path:  []string{"/api/v1/namespaces/ns1/pods/p1"},
			cols:  []string{"Name", "Ready", "Status", "Age"},
			cells: []any{"p1", "1/2", "Running"},
			obj:   "PartialObjectMetadata",
		},
		"deploy-object": {
			gv:      appsv1.SchemeGroupVersion,
			path:    []string{"/apis/apps/v1/namespaces/ns1/deployments"},
			include: "Object",
			cols:    []string{"Name", "Ready", "Age"},
			cells:   []any{"dp1", "1/2"},
			obj:     "Deployment",
		},
		"events": {
			gv:    eventsv1.SchemeGroupVersion,
			path:  []string{"/apis/events.k8s.io/v1/namespaces/ns1/events/e1"},
			cols:  []string{"Last Seen", "Type", "Reason", "Object", "Message"},
			cells: []any{"<unknown>", "Warning", "Boom", "pod/p1", "it broke"},
			obj:   "PartialObjectMetadata",
		},
		"core-events": {
			gv:    corev1.SchemeGroupVersion,
			path:  []string{"/api/v1/namespaces/ns1/events/e2"},
			cells: []any{"<unknown>", "Normal", "", "pod/p2", ""},
			cols:  []string{"Last Seen", "Type", "Reason", "Object", "Message"},
			obj:   "PartialObjectMetadata",
		},
		"pc": {
			gv:    schedulingv1.SchemeGroupVersion,
			path:  []string{"/apis/scheduling.k8s.io/v1/priorityclasses/pc1"},
			cols:  []string{"Name", "Value", "Description", "Age"},
			cells: []any{"pc1", int64(10), ""},
			obj:   "PartialObjectMetadata",
		},
		"rs": {
			gv:    appsv1.SchemeGroupVersion,
			path:  []string{"/apis/apps/v1/namespaces/ns1/replicasets/rs1"},
			cols:  []string{"Name", "Desired", "Current", "Ready", "Age"},
			cells: []any{"rs1", int64(3), int64(0), int64(0), "60m"},
			obj:   "PartialObjectMetadata",
		},
		"ds": {
			gv:    appsv1.SchemeGroupVersion,
			path:  []string{"/apis/apps/v1/namespaces/ns1/daemonsets/ds1"},
			cols:  []string{"Name", "Desired", "Current", "Ready", "Age"},
			cells: []any{"ds1", int64(2), int64(0), int64(1)},
			obj:   "PartialObjectMetadata",
		},
		"ns-none": {
			gv:      corev1.SchemeGroupVersion,
			path:    []string{"/api/v1/namespaces/default"},
			include: "None",
			cols:    []string{"Name", "Age"},
			cells:   []any{"default"},
		},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			tbl, err := getTable(t, tableClient(t, cfg, u.gv), u.include, u.path...)
			require.NoError(t, err)
			cols := make([]string, 0, len(tbl.ColumnDefinitions))
			for _, c := range tbl.ColumnDefinitions {
				cols = append(cols, c.Name)
			}
			assert.Equal(t, u.cols, cols)
			require.NotEmpty(t, tbl.Rows)
			assert.Equal(t, u.cells, tbl.Rows[0].Cells[:len(u.cells)])
			if u.obj == "" {
				assert.Nil(t, tbl.Rows[0].Object.Raw)
				return
			}
			assert.Contains(t, string(tbl.Rows[0].Object.Raw), `"kind":"`+u.obj+`"`)
		})
	}
}

func TestAPIServerMutations(t *testing.T) {
	fc, cs, _ := apiCluster(t)
	ctx := context.Background()

	_, err := cs.CoreV1().Pods("ns1").Patch(ctx, "p1", types.MergePatchType,
		[]byte(`{"metadata":{"labels":{"x":"y"}}}`), metav1.PatchOptions{})
	require.NoError(t, err)
	u, err := fc.DynGet(client.PodGVR, "ns1", "p1")
	require.NoError(t, err)
	assert.Equal(t, "y", u.GetLabels()["x"])

	_, err = cs.CoreV1().ConfigMaps("ns1").Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cm1"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = fc.DynGet(client.CmGVR, "ns1", "cm1")
	require.NoError(t, err)

	cm, err := cs.CoreV1().ConfigMaps("ns1").Get(ctx, "cm1", metav1.GetOptions{})
	require.NoError(t, err)
	cm.Data = map[string]string{"a": "b"}
	_, err = cs.CoreV1().ConfigMaps("ns1").Update(ctx, cm, metav1.UpdateOptions{})
	require.NoError(t, err)

	fc.Reset()
	fg := metav1.DeletePropagationForeground
	require.NoError(t, cs.CoreV1().Pods("ns1").Delete(ctx, "p2", metav1.DeleteOptions{
		PropagationPolicy: &fg, GracePeriodSeconds: ptr.To[int64](0),
	}))
	assert.Equal(t, []fakecluster.DeleteCall{{
		GVR: client.PodGVR.GVR(), NS: "ns1", Name: "p2", Propagation: &fg,
		Grace: ptr.To[int64](0), Source: fakecluster.SourceDyn,
	}}, fc.Deletes())

	err = cs.CoreV1().Pods("ns1").Delete(ctx, "p2", metav1.DeleteOptions{})
	assert.True(t, apierrors.IsNotFound(err), "got %v", err)
}

func TestAPIServerScale(t *testing.T) {
	fc, cs, _ := apiCluster(t)
	ctx := context.Background()

	s, err := cs.AppsV1().Deployments("ns1").GetScale(ctx, "dp1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, int32(2), s.Spec.Replicas)

	s.Spec.Replicas = 5
	s, err = cs.AppsV1().Deployments("ns1").UpdateScale(ctx, "dp1", s, metav1.UpdateOptions{})
	require.NoError(t, err)
	assert.Equal(t, int32(5), s.Spec.Replicas)
	dp, err := fc.DynGet(client.DpGVR, "ns1", "dp1")
	require.NoError(t, err)
	r, _, _ := unstructured.NestedInt64(dp.Object, "spec", "replicas")
	assert.Equal(t, int64(5), r)
	assert.Len(t, fc.Actions("update", "deployments"), 1)

	_, err = cs.AppsV1().Deployments("ns1").GetScale(ctx, "nope", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "got %v", err)
	_, err = cs.AppsV1().Deployments("ns1").UpdateScale(ctx, "nope",
		&autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: "nope"}}, metav1.UpdateOptions{})
	assert.True(t, apierrors.IsNotFound(err), "got %v", err)
}

func TestAPIServerErrors(t *testing.T) {
	boom := errors.New("boom")
	fc, cs, cfg := apiCluster(t,
		fakecluster.DenyNS("ns2"),
		fakecluster.FailOn("list", "configmaps", 0, boom),
		fakecluster.WithNamespaces("ns2"),
	)
	ctx := context.Background()

	_, err := cs.CoreV1().Pods("ns2").List(ctx, metav1.ListOptions{})
	assert.True(t, apierrors.IsForbidden(err), "got %v", err)

	_, err = cs.CoreV1().ConfigMaps("ns1").List(ctx, metav1.ListOptions{})
	assert.True(t, apierrors.IsInternalError(err), "got %v", err)
	assert.Contains(t, err.Error(), "boom")

	_, err = cs.CoreV1().Pods("ns1").Watch(ctx, metav1.ListOptions{})
	assert.True(t, apierrors.IsMethodNotSupported(err), "got %v", err)

	rc := tableClient(t, cfg, corev1.SchemeGroupVersion)
	for _, p := range []string{"/nope", "/api/v1/fred", "/api/v1/namespaces/ns1/pods/p1/log", "/api/v1/a/b/c/d"} {
		err := rc.Get().AbsPath(p).Do(ctx).Error()
		assert.True(t, apierrors.IsNotFound(err), "%s: got %v", p, err)
	}
	for _, m := range []string{http.MethodPatch, http.MethodDelete} {
		err := rc.Verb(m).AbsPath("/api/v1/namespaces/ns1/pods").Do(ctx).Error()
		assert.True(t, apierrors.IsMethodNotSupported(err), "%s: got %v", m, err)
	}
	for _, m := range []string{http.MethodPost, http.MethodDelete} {
		err := rc.Verb(m).AbsPath("/api/v1/namespaces/ns1/pods/p1").Body([]byte("{bad")).Do(ctx).Error()
		assert.True(t, apierrors.IsBadRequest(err) || apierrors.IsMethodNotSupported(err), "%s: got %v", m, err)
	}
	err = rc.Put().AbsPath("/apis/apps/v1/namespaces/ns1/deployments/dp1/scale").Body([]byte("{bad")).Do(ctx).Error()
	assert.True(t, apierrors.IsBadRequest(err), "got %v", err)
	err = rc.Put().AbsPath("/api/v1/namespaces/ns1/pods/p1").Body([]byte("{bad")).Do(ctx).Error()
	assert.True(t, apierrors.IsBadRequest(err), "got %v", err)
	assert.NotEmpty(t, fc.RESTCalls())
}

func TestAPIServerIntercept(t *testing.T) {
	fc, cs, _ := apiCluster(t)
	fc.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/pods") {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":`))
		return true
	})

	_, err := cs.CoreV1().Pods("ns1").List(context.Background(), metav1.ListOptions{})
	require.Error(t, err)
	_, err = cs.CoreV1().Pods("ns1").Get(context.Background(), "p1", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestAPIServerCancellation(t *testing.T) {
	fc, cs, _ := apiCluster(t)
	entered, canceled := make(chan struct{}), make(chan struct{})
	fc.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/pods") {
			return false
		}
		close(entered)
		<-r.Context().Done()
		close(canceled)
		return true
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cs.CoreV1().Pods("ns1").List(ctx, metav1.ListOptions{})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request never reached server")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("client did not cancel")
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not release canceled request")
	}
}
