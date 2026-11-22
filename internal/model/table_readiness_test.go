// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestReadinessKeepsMetadataRequests(t *testing.T) {
	gvr := client.NewGVR("readiness.example.test/v1/widgets")
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "readiness.example.test/v1", "kind": "Widget", "metadata": map[string]any{"name": "sample", "namespace": "test"}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False"}}}}}
	fc := fakecluster.New(t, fakecluster.WithAPIServer(), fakecluster.WithCRD(gvr, "Widget", true), fakecluster.WithObjects(obj))
	queries := make(chan string, 8)
	fc.Intercept(func(_ http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/widgets") {
			queries <- r.URL.Query().Get("includeObject")
		}
		return false
	})
	factory := watch.NewFactory(fc.Conn())
	t.Cleanup(factory.Terminate)
	ctx := context.WithValue(context.Background(), internal.KeyFactory, factory)
	table := NewTable(gvr)
	table.SetNamespace("test")
	disabled, enabled := false, true
	for _, setting := range []*config.ViewSetting{nil, {Readiness: &disabled}, {Readiness: &enabled}} {
		table.SetViewSetting(context.Background(), setting)
		require.NoError(t, table.reconcile(ctx))
		assert.Equal(t, "Metadata", <-queries)
	}
	table.SetViewSetting(context.Background(), &config.ViewSetting{Columns: []string{"NAME", "READY:.status.conditions[0].status"}})
	require.NoError(t, table.reconcile(ctx))
	assert.Equal(t, "Object", <-queries)
	// Removing columns returns to the metadata path even while readiness stays on.
	table.SetViewSetting(context.Background(), &config.ViewSetting{Readiness: &enabled})
	require.NoError(t, table.reconcile(ctx))
	assert.Equal(t, "Metadata", <-queries)
}
