// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/globlit/k9s/internal/xray"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	descCalls atomic.Int32
	descMx    sync.Mutex
	descOut   string
	descErr   error
)

func setDescribe(out string, err error) {
	descMx.Lock()
	defer descMx.Unlock()
	descOut, descErr = out, err
}

type fakeDescriber struct {
	dao.Resource
}

func (*fakeDescriber) Describe(string) (string, error) {
	descCalls.Add(1)
	descMx.Lock()
	defer descMx.Unlock()

	return descOut, descErr
}

type viewerRec struct {
	mx      sync.Mutex
	changes [][]string
	fails   []error
}

func (r *viewerRec) ResourceChanged(lines []string, _ fuzzy.Matches) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.changes = append(r.changes, lines)
}

func (r *viewerRec) ResourceFailed(err error) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.fails = append(r.fails, err)
}

func (r *viewerRec) counts() (int, int) {
	r.mx.Lock()
	defer r.mx.Unlock()

	return len(r.changes), len(r.fails)
}

func newModelFactory(t *testing.T, opts ...fakecluster.Option) (*fakecluster.Cluster, *watch.Factory, context.Context) {
	t.Helper()
	fc := fakecluster.New(t, opts...)
	f := watch.NewFactory(fc.Conn())
	f.Start(client.NamespaceAll)
	t.Cleanup(f.Terminate)

	return fc, f, context.WithValue(context.Background(), internal.KeyFactory, f)
}

func useFakeDescriber(t *testing.T) {
	t.Helper()
	old, ok := Registry[client.CmGVR]
	Registry[client.CmGVR] = ResourceMeta{DAO: new(fakeDescriber)}
	dao.MetaAccess.RegisterMeta(client.CmGVR.String(), &metav1.APIResource{Name: "configmaps", Kind: "ConfigMap", Namespaced: true})
	descCalls.Store(0)
	setDescribe("a\nb", nil)
	t.Cleanup(func() {
		if ok {
			Registry[client.CmGVR] = old
		} else {
			delete(Registry, client.CmGVR)
		}
	})
}

func cm(rv string, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "cm1", ResourceVersion: rv},
		Data:       map[string]string{"k": data},
	}
}

func setCM(t *testing.T, fc *fakecluster.Cluster, f *watch.Factory, rv string) {
	t.Helper()
	cs, _ := fc.Conn().Dial()
	_, err := cs.CoreV1().ConfigMaps("ns1").Update(context.Background(), cm(rv, rv), metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		o, err := f.Get(client.CmGVR, "ns1/cm1", false, labels.Everything())
		if err != nil {
			return false
		}
		m, err := meta.Accessor(o)
		return err == nil && m.GetResourceVersion() == rv
	}, 5*time.Second, 10*time.Millisecond)
}

func TestDescribeSkipsUnchangedObject(t *testing.T) {
	useFakeDescriber(t)
	fc, f, ctx := newModelFactory(t, fakecluster.MirrorTypedToDyn(), fakecluster.WithObjects(cm("1", "1")))
	_, err := f.List(client.CmGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)

	d := NewDescribe(client.CmGVR, "ns1/cm1")
	var rec viewerRec
	d.AddListener(&rec)

	require.NoError(t, d.Refresh(ctx))
	assert.Equal(t, int32(1), descCalls.Load())
	assert.Equal(t, []string{"a", "b"}, d.Peek())

	require.NoError(t, d.refresh(ctx))
	require.NoError(t, d.refresh(ctx))
	assert.Equal(t, int32(1), descCalls.Load(), "skip describe when the object is unchanged")

	setCM(t, fc, f, "2")
	require.NoError(t, d.refresh(ctx))
	assert.Equal(t, int32(2), descCalls.Load(), "describe again on a new resource version")
	ch, _ := rec.counts()
	assert.Equal(t, 1, ch, "skip listeners when the output is unchanged")

	setDescribe("a\nc", nil)
	d.lastDescribe = time.Now().Add(-2 * describeMaxAge)
	require.NoError(t, d.refresh(ctx))
	assert.Equal(t, int32(3), descCalls.Load(), "describe again past the max age")
	ch, _ = rec.counts()
	assert.Equal(t, 2, ch)
	assert.Equal(t, []string{"a", "c"}, d.Peek())

	d.Toggle()
	require.NoError(t, d.refresh(ctx))
	assert.Equal(t, int32(4), descCalls.Load(), "describe again after a toggle")
}

func TestDescribeFailureResets(t *testing.T) {
	useFakeDescriber(t)
	_, f, ctx := newModelFactory(t, fakecluster.WithObjects(cm("1", "1")))
	_, err := f.List(client.CmGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)

	d := NewDescribe(client.CmGVR, "ns1/cm1")
	var rec viewerRec
	d.AddListener(&rec)
	require.NoError(t, d.Refresh(ctx))

	setDescribe("a\nb", errors.New("boom"))
	d.Toggle()
	require.Error(t, d.refresh(ctx))
	assert.Nil(t, d.Peek())
	_, fails := rec.counts()
	assert.Equal(t, 1, fails)

	setDescribe("a\nb", nil)
	require.NoError(t, d.refresh(ctx))
	assert.Equal(t, int32(3), descCalls.Load(), "describe again after a failure")
	assert.Equal(t, []string{"a", "b"}, d.Peek())

	d.RemoveListener(&rec)
	d.Filter("b")
	ch, _ := rec.counts()
	assert.Equal(t, 2, ch)
}

func TestDescribeNoFactory(t *testing.T) {
	useFakeDescriber(t)
	d := NewDescribe(client.CmGVR, "ns1/cm1")
	require.Error(t, d.Refresh(context.Background()))
	assert.Empty(t, d.resourceVersion(context.Background()))
}

func TestValuesRefreshFiresOnce(t *testing.T) {
	v := NewValues(client.HmGVR, "ns1/r1")
	v.lines = []string{"a: 1", "b: 2"}
	var rec viewerRec
	v.AddListener(&rec)

	require.NoError(t, v.Watch(context.Background()))
	ch, _ := rec.counts()
	assert.Equal(t, 1, ch, "fetch once on watch: no polling")

	v.Filter("b")
	ch, _ = rec.counts()
	assert.Equal(t, 2, ch)
	v.ClearFilter()
	assert.Equal(t, []string{"a: 1", "b: 2"}, v.Peek())

	atomic.StoreInt32(&v.inUpdate, 1)
	require.Error(t, v.Refresh(context.Background()), "drop overlapping refreshes")
	atomic.StoreInt32(&v.inUpdate, 0)

	v.RemoveListener(&rec)
	require.NoError(t, v.Refresh(context.Background()))
	ch, _ = rec.counts()
	assert.Equal(t, 2, ch)
}

type revValuer struct {
	dao.HelmHistory
	path string
}

func (r *revValuer) GetValues(path string, _ bool) ([]byte, error) {
	r.path = path
	return []byte("a: 1"), nil
}

func TestRevValuesUsesRevision(t *testing.T) {
	old := Registry[client.HmhGVR]
	t.Cleanup(func() { Registry[client.HmhGVR] = old })
	var r revValuer
	m := old
	m.DAO = &r
	Registry[client.HmhGVR] = m

	v := NewRevValues(client.HmhGVR, "ns1/r1", "3")
	assert.Equal(t, "ns1/r1:3", r.path)
	assert.Equal(t, []string{"a: 1"}, v.Peek())
}

type tableRec struct {
	mx      sync.Mutex
	changed []int
	noData  int
	failed  []error
}

func (r *tableRec) TableNoData(*model1.TableData) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.noData++
}

func (r *tableRec) TableDataChanged(d *model1.TableData) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.changed = append(r.changed, d.RowCount())
}

func (r *tableRec) TableLoadFailed(err error) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.failed = append(r.failed, err)
}

func dp(ns, n string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: n, Labels: map[string]string{"app": n}}}
}

func TestTableRefreshFakeCluster(t *testing.T) {
	_, f, ctx := newModelFactory(t, fakecluster.WithObjects(dp("ns1", "d1"), dp("ns1", "d2"), dp("ns2", "d3")))
	_, err := f.List(client.DpGVR, client.BlankNamespace, true, labels.Everything())
	require.NoError(t, err)

	uu := map[string]struct {
		ns  string
		sel string
		e   int
	}{
		"all":   {ns: client.NamespaceAll, e: 3},
		"ns1":   {ns: "ns1", e: 2},
		"label": {ns: client.NamespaceAll, sel: "app=d3", e: 1},
		"empty": {ns: "ns3", e: 0},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			ta := NewTable(client.DpGVR)
			ta.SetNamespace(u.ns)
			if u.sel != "" {
				sel, err := labels.Parse(u.sel)
				require.NoError(t, err)
				ta.SetLabelSelector(sel)
			}
			var rec tableRec
			ta.AddListener(&rec)
			require.Eventually(t, func() bool {
				return ta.Refresh(ctx) == nil && ta.RowCount() == u.e
			}, 5*time.Second, 10*time.Millisecond)
			if u.e == 0 {
				assert.Positive(t, rec.noData)
			} else {
				assert.Contains(t, rec.changed, u.e)
			}
		})
	}
}

func TestTableRefreshDenied(t *testing.T) {
	_, _, ctx := newModelFactory(t, fakecluster.DenyVerb(client.ListVerb))

	ta := NewTable(client.DpGVR)
	ta.SetNamespace("ns1")
	require.ErrorContains(t, ta.Refresh(ctx), "access denied")
}

func TestTableGetFakeCluster(t *testing.T) {
	_, f, ctx := newModelFactory(t, fakecluster.WithObjects(dp("ns1", "d1")))
	_, err := f.List(client.DpGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)

	ta := NewTable(client.DpGVR)
	ta.SetNamespace("ns1")
	o, err := ta.Get(ctx, "ns1/d1")
	require.NoError(t, err)
	assert.NotNil(t, o)

	ta.SetInstance("ns1/d1")
	require.NoError(t, ta.Refresh(ctx))
	assert.Equal(t, 1, ta.RowCount())
}

type treeRec struct {
	mx     sync.Mutex
	roots  []*xray.TreeNode
	failed []error
}

func (r *treeRec) TreeChanged(n *xray.TreeNode) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.roots = append(r.roots, n)
}

func (r *treeRec) TreeLoadFailed(err error) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.failed = append(r.failed, err)
}

func TestTreeRefreshFakeCluster(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "d1"}}
	d := dp("ns1", "d1")
	d.Spec.Selector = sel
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1", Labels: sel.MatchLabels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}},
	}
	_, f, ctx := newModelFactory(t, fakecluster.WithObjects(d, p))
	for _, gvr := range []*client.GVR{client.DpGVR, client.PodGVR} {
		_, err := f.List(gvr, "ns1", true, labels.Everything())
		require.NoError(t, err)
	}

	tr := NewTree(client.DpGVR)
	tr.SetNamespace("ns1")
	var rec treeRec
	tr.AddListener(&rec)

	tr.Refresh(ctx)
	require.Len(t, rec.roots, 1)
	assert.NotNil(t, tr.Peek().Find(client.PodGVR, "ns1/p1"))

	tr.Refresh(ctx)
	assert.Len(t, rec.roots, 1, "skip listeners when the tree is unchanged")
	assert.Empty(t, rec.failed)

	tr.SetFilter("zorg")
	tr.Refresh(ctx)
	assert.Len(t, rec.roots, 1, "keep the stored tree unfiltered")

	tr.Refresh(context.Background())
	assert.Len(t, rec.failed, 1, "report a missing factory")
}

func TestPulseHealthCheck(t *testing.T) {
	ok := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "ok"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{Name: "c1", Ready: true}},
		},
	}
	bad := ok.DeepCopy()
	bad.Name = "bad"
	bad.Status.Phase = corev1.PodFailed
	bad.Status.ContainerStatuses[0].Ready = false

	_, f, ctx := newModelFactory(t, fakecluster.WithObjects(ok, bad))
	for _, gvr := range PulseGVRs[:3] {
		_, err := f.List(gvr, client.BlankNamespace, true, labels.Everything())
		require.NoError(t, err, gvr.String())
	}
	_, err := f.List(client.PodGVR, client.BlankNamespace, true, labels.Everything())
	require.NoError(t, err)
	ctx = context.WithValue(ctx, internal.KeyWithMetrics, false)

	h := NewPulseHealth(f)
	hp, err := h.check(ctx, client.BlankNamespace, client.PodGVR)
	require.NoError(t, err)
	assert.Equal(t, HealthPoint{GVR: client.PodGVR, Total: 2, Faults: 1}, hp)

	// Events use the table DAO: it calls the REST API, and the fake cluster has none.
	c := make(HealthChan, len(PulseGVRs))
	require.Error(t, h.checkPulse(ctx, client.BlankNamespace, c))
	assert.Len(t, c, 3, "send the points before the failure")
}

func TestPulseHealthCheckDenied(t *testing.T) {
	_, f, ctx := newModelFactory(t, fakecluster.DenyVerb(client.ListVerb))

	h := NewPulseHealth(f)
	_, err := h.check(ctx, "ns1", client.PodGVR)
	require.Error(t, err)
	require.Error(t, h.checkPulse(ctx, "ns1", make(HealthChan, len(PulseGVRs))))
}

func TestPulseNamespace(t *testing.T) {
	p := NewPulse(client.PuGVR)
	p.SetNamespace(client.NamespaceAll)
	assert.Equal(t, client.BlankNamespace, p.GetNamespace())
	p.SetNamespace("ns1")
	assert.Equal(t, "ns1", p.GetNamespace())

	_, _, err := p.Watch(context.Background())
	require.Error(t, err)
}

type stackComp struct {
	Component
	name string
}

func (c stackComp) Name() string { return c.name }
func (stackComp) Start()         {}
func (stackComp) Stop()          {}

func TestStackConcurrent(t *testing.T) {
	s := NewStack()

	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 50 {
				s.Push(stackComp{name: fmt.Sprintf("c%d-%d", g, i)})
				_ = s.Flatten()
				_ = s.Top()
				_ = s.Peek()
				_ = s.IsLast()
				s.Pop()
			}
		})
	}
	wg.Wait()
	assert.True(t, s.Empty())
}

func TestTableConcurrentRefresh(t *testing.T) {
	oo := make([]runtime.Object, 0, 20)
	for i := range 20 {
		oo = append(oo, dp("ns1", fmt.Sprintf("d%d", i)))
	}
	_, f, ctx := newModelFactory(t, fakecluster.WithObjects(oo...))
	_, err := f.List(client.DpGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)

	ta := NewTable(client.DpGVR)
	ta.SetNamespace("ns1")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			var rec tableRec
			for range 20 {
				_ = ta.Refresh(ctx)
				ta.AddListener(&rec)
				_ = ta.Peek()
				_ = ta.GetLabelSelector()
				ta.RemoveListener(&rec)
			}
		})
	}
	wg.Wait()
	require.NoError(t, ta.Refresh(ctx))
	assert.Equal(t, 20, ta.RowCount())
}
