// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/slogs"
	"github.com/globlit/k9s/internal/xray"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const initTreeRefreshRate = 500 * time.Millisecond

// TreeListener represents a tree model listener.
type TreeListener interface {
	// TreeChanged notifies the model data changed.
	TreeChanged(*xray.TreeNode)

	// TreeLoadFailed notifies the load failed.
	TreeLoadFailed(error)
}

// Tree represents a tree model.
type Tree struct {
	gvr         *client.GVR
	namespace   string
	root        *xray.TreeNode
	rootMx      sync.RWMutex
	listeners   []TreeListener
	inUpdate    int32
	refreshRate time.Duration
	query       string
}

// NewTree returns a new model.
func NewTree(gvr *client.GVR) *Tree {
	return &Tree{
		gvr:         gvr,
		refreshRate: 2 * time.Second,
	}
}

// ClearFilter clears out active filter.
func (t *Tree) ClearFilter() {
	t.query = ""
}

// SetFilter sets the current filter.
func (t *Tree) SetFilter(q string) {
	t.query = q
}

// AddListener adds a listener.
func (t *Tree) AddListener(l TreeListener) {
	t.listeners = append(t.listeners, l)
}

// RemoveListener delete a listener.
func (t *Tree) RemoveListener(l TreeListener) {
	victim := -1
	for i, lis := range t.listeners {
		if lis == l {
			victim = i
			break
		}
	}

	if victim >= 0 {
		t.listeners = append(t.listeners[:victim], t.listeners[victim+1:]...)
	}
}

// Watch initiates model updates.
func (t *Tree) Watch(ctx context.Context) {
	t.Refresh(ctx)
	go t.updater(ctx)
}

// Refresh update the model now.
func (t *Tree) Refresh(ctx context.Context) {
	t.refresh(ctx)
}

// GetNamespace returns the model namespace.
func (t *Tree) GetNamespace() string {
	return t.namespace
}

// SetNamespace sets up model namespace.
func (t *Tree) SetNamespace(ns string) {
	t.namespace = ns
	if root := t.getRoot(); root != nil {
		root.Clear()
	}
}

// SetRefreshRate sets model refresh duration.
func (t *Tree) SetRefreshRate(d time.Duration) {
	t.refreshRate = d
}

// ClusterWide checks if resource is scope for all namespaces.
func (t *Tree) ClusterWide() bool {
	return client.IsClusterWide(t.namespace)
}

// InNamespace checks if current namespace matches desired namespace.
func (t *Tree) InNamespace(ns string) bool {
	return t.namespace == ns
}

// Empty return true if no model data.
func (t *Tree) Empty() bool {
	return t.getRoot().IsLeaf()
}

// Peek returns model data.
func (t *Tree) Peek() *xray.TreeNode {
	return t.getRoot()
}

func (t *Tree) getRoot() *xray.TreeNode {
	t.rootMx.RLock()
	defer t.rootMx.RUnlock()

	return t.root
}

func (t *Tree) setRoot(root *xray.TreeNode) {
	t.rootMx.Lock()
	t.root = root
	t.rootMx.Unlock()
}

// Describe describes a given resource.
func (t *Tree) Describe(ctx context.Context, gvr *client.GVR, path string) (string, error) {
	meta, err := t.getMeta(ctx, gvr)
	if err != nil {
		return "", err
	}

	desc, ok := meta.DAO.(dao.Describer)
	if !ok {
		return "", fmt.Errorf("no describer for %q", meta.DAO.GVR())
	}

	return desc.Describe(path)
}

// ToYAML returns a resource yaml.
func (t *Tree) ToYAML(ctx context.Context, gvr *client.GVR, path string) (string, error) {
	meta, err := t.getMeta(ctx, gvr)
	if err != nil {
		return "", err
	}

	desc, ok := meta.DAO.(dao.Describer)
	if !ok {
		return "", fmt.Errorf("no describer for %q", meta.DAO.GVR())
	}

	return desc.ToYAML(path, false)
}

func (t *Tree) updater(ctx context.Context) {
	defer internal.LogPanic("tree updater")
	defer slog.Debug("Tree-model canceled", slogs.GVR, t.gvr)

	rate := initTreeRefreshRate
	for {
		select {
		case <-ctx.Done():
			t.setRoot(nil)
			return
		case <-time.After(rate):
			rate = t.refreshRate
			t.refresh(ctx)
		}
	}
}

func (t *Tree) refresh(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&t.inUpdate, 0, 1) {
		slog.Debug("Dropping update...")
		return
	}
	defer atomic.StoreInt32(&t.inUpdate, 0)

	if err := t.reconcile(ctx); err != nil {
		slog.Error("Reconcile failed", slogs.Error, err)
		t.fireTreeLoadFailed(err)
		return
	}
}

func (t *Tree) list(ctx context.Context, a dao.Accessor) ([]runtime.Object, error) {
	factory, ok := ctx.Value(internal.KeyFactory).(dao.Factory)
	if !ok {
		return nil, fmt.Errorf("expected Factory in context but got %T", ctx.Value(internal.KeyFactory))
	}
	a.Init(factory, t.gvr)

	return a.List(ctx, client.CleanseNamespace(t.namespace))
}

func (t *Tree) reconcile(ctx context.Context) error {
	meta := t.resourceMeta()
	oo, err := t.list(ctx, meta.DAO)
	if err != nil {
		return err
	}

	ns := client.CleanseNamespace(t.namespace)
	root := xray.NewTreeNode(t.gvr, t.gvr.R())
	ctx = context.WithValue(ctx, xray.KeyParent, root)
	if _, ok := meta.TreeRenderer.(*xray.Generic); ok {
		table, ok := oo[0].(*metav1.Table)
		if !ok {
			return fmt.Errorf("expecting a Table but got %T", oo[0])
		}
		if err := genericTreeHydrate(ctx, ns, table, meta.TreeRenderer); err != nil {
			return err
		}
	} else if err := treeHydrate(ctx, ns, oo, meta.TreeRenderer); err != nil {
		return err
	}

	root.Sort()
	// Keep the tree unfiltered: views filter it. Diffing filtered against
	// unfiltered trees redraws on every tick.
	if old := t.getRoot(); old == nil || old.Diff(root) {
		t.setRoot(root)
		t.fireTreeChanged(root)
	}

	return nil
}

func (t *Tree) resourceMeta() ResourceMeta {
	return resourceMeta(t.gvr)
}

func (t *Tree) fireTreeChanged(root *xray.TreeNode) {
	for _, l := range t.listeners {
		l.TreeChanged(root)
	}
}

func (t *Tree) fireTreeLoadFailed(err error) {
	for _, l := range t.listeners {
		l.TreeLoadFailed(err)
	}
}

func (t *Tree) getMeta(ctx context.Context, gvr *client.GVR) (ResourceMeta, error) {
	meta := t.resourceMeta()
	factory, ok := ctx.Value(internal.KeyFactory).(dao.Factory)
	if !ok {
		return ResourceMeta{}, fmt.Errorf("expected Factory in context but got %T", ctx.Value(internal.KeyFactory))
	}
	meta.DAO.Init(factory, gvr)

	return meta, nil
}

// ----------------------------------------------------------------------------
// Helpers...

func treeHydrate(ctx context.Context, ns string, oo []runtime.Object, re TreeRenderer) error {
	if re == nil {
		return fmt.Errorf("no tree renderer defined for this resource")
	}
	root, ok := ctx.Value(xray.KeyParent).(*xray.TreeNode)
	if !ok {
		return fmt.Errorf("expecting a TreeNode but got %T", ctx.Value(xray.KeyParent))
	}

	// Render each object into its own root, then merge sequentially: renderers
	// find-or-create parent nodes and race on a shared root.
	scratch := make([]*xray.TreeNode, len(oo))
	pool := internal.NewWorkerPool(ctx, internal.DefaultPoolSize)
	for i, o := range oo {
		pool.Add(func(_ context.Context) error {
			sr := xray.NewTreeNode(root.GVR, root.ID)
			scratch[i] = sr
			return re.Render(context.WithValue(ctx, xray.KeyParent, sr), ns, o)
		})
	}
	errs := pool.Drain()
	for _, sr := range scratch {
		mergeTree(root, sr)
	}
	if len(errs) > 0 {
		return errs[0]
	}

	return nil
}

// mergeTree moves src children under dst, folding nodes with equal identity.
func mergeTree(dst, src *xray.TreeNode) {
	if src == nil {
		return
	}
	idx := make(map[string]*xray.TreeNode, len(dst.Children))
	for _, c := range dst.Children {
		idx[c.GVR.String()+"|"+c.ID] = c
	}
	for _, c := range src.Children {
		k := c.GVR.String() + "|" + c.ID
		if cur, ok := idx[k]; ok && c.GVR == client.NsGVR {
			for _, gc := range c.Children {
				cur.Add(gc)
			}
			continue
		}
		dst.Add(c)
		idx[k] = c
	}
}

func genericTreeHydrate(ctx context.Context, ns string, table *metav1.Table, re TreeRenderer) error {
	tre, ok := re.(*xray.Generic)
	if !ok {
		return fmt.Errorf("expecting xray.Generic renderer but got %T", re)
	}

	tre.SetTable(ns, table)
	// BOZO!! Need table row sorter!!
	for _, row := range table.Rows {
		if err := tre.Render(ctx, ns, row); err != nil {
			return err
		}
	}

	return nil
}
