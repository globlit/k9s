// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"fmt"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/model1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestReadyFromCells(t *testing.T) {
	for _, tc := range []struct {
		column  string
		value   any
		invalid bool
	}{
		{"READY", false, true}, {"Synced", "Unknown", true}, {"AVAILABLE", "False", true},
		{"HEALTHY", "Healthy", false}, {"STATUS", "Unhealthy", true},
		{"STATUS", "NotFailed", false}, {"STATUS", "Cluster in degraded state", true},
		{"STATUS", "Error", true}, {"STATUS", "Stalled", true}, {"STATUS", "Failed", true},
		{"READY", "0/2", true}, {"READY", "0/0", false}, {"READY", "1/2", false},
		{"READY", "x/2", false}, {"NAME", "Failed", false}, {"READY", nil, false},
	} {
		t.Run(tc.column+"_"+fmt.Sprint(tc.value), func(t *testing.T) {
			got := readyFromCells(model1.Header{{Name: tc.column}}, []any{tc.value})
			assert.Equal(t, tc.invalid, got != "", got)
		})
	}
	assert.Empty(t, readyFromCells(model1.Header{{Name: "READY"}}, nil))
}

func TestReadyFromConditions(t *testing.T) {
	for _, tc := range []struct {
		name, typ, status string
		observed          int64
		want              string
		known             bool
	}{
		{"ready", "Ready", "True", 2, "", true},
		{"failure", "Ready", "False", 2, "Ready: Broken: try again", true},
		{"unknown", "Healthy", "Unknown", 2, "Healthy: Broken: try again", true},
		{"unrecognized", "Other", "False", 2, "", false},
		{"missing", "Ready", "", 2, "", false},
		{"stale", "Ready", "False", 1, "Reconciling", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := map[string]any{"metadata": map[string]any{"generation": int64(2)}, "status": map[string]any{"conditions": []any{
				map[string]any{"type": tc.typ, "status": tc.status, "observedGeneration": tc.observed, "reason": "Broken", "message": "try again"},
			}}}
			got, known := readyFromConditions(obj)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.known, known)
		})
	}
	got, known := readyFromConditions(map[string]any{"metadata": map[string]any{"generation": int64(3)}, "status": map[string]any{"observedGeneration": int64(2)}})
	assert.Equal(t, "Reconciling", got)
	assert.True(t, known)
	got, known = readyFromConditions(nil)
	assert.Empty(t, got)
	assert.False(t, known)
}

func TestTableReadiness(t *testing.T) {
	disabled := false
	for _, tc := range []struct {
		name      string
		vs        *config.ViewSetting
		condition string
		raw       bool
		want      string
	}{
		{name: "cells", want: "READY: False"},
		{name: "metadata raw", raw: true, want: "READY: False"},
		{name: "current condition wins", condition: "True"},
		{name: "condition fault", condition: "False", want: "Ready: False"},
		{name: "disabled", vs: &config.ViewSetting{Readiness: &disabled}},
		{name: "custom", vs: &config.ViewSetting{Columns: []string{"NAME", "VALID", "READY", "AGE"}}, want: "READY: False"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "sample", "namespace": "test"}}}
			if tc.condition != "" {
				obj.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": tc.condition}}}
			}
			row := metav1.TableRow{Cells: []any{"sample", "False", "1m"}, Object: runtime.RawExtension{Object: obj}}
			if tc.raw {
				row.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"sample","namespace":"test"}}`)}
			}
			table := &metav1.Table{ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Name"}, {Name: "Ready"}, {Name: "Age"}}, Rows: []metav1.TableRow{row}}
			var renderer Table
			renderer.SetViewSetting(tc.vs)
			renderer.SetTable("test", table)
			var result model1.Row
			require.NoError(t, renderer.Render(row, "test", &result))
			h := renderer.Header("test")
			assert.Len(t, result.Fields, len(h))
			assert.Equal(t, "test/sample", result.ID)
			col := "VALID"

			idx, ok := h.IndexOf(col, true)
			require.True(t, ok)
			assert.Equal(t, tc.want, result.Fields[idx])
		})
	}
}

func TestTableKeepsPrinterValidity(t *testing.T) {
	table := &metav1.Table{ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Name"}, {Name: "VALID"}}, Rows: []metav1.TableRow{{Cells: []any{"sample", "true"}}}}
	var renderer Table
	renderer.SetTable("test", table)
	var row model1.Row
	require.NoError(t, renderer.Render(table.Rows[0], "test", &row))
	assert.Len(t, renderer.Header("test"), 2)
	assert.Equal(t, model1.Fields{"sample", "true"}, row.Fields)
}

func TestGenericReadinessOptOut(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "sample"}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False"}}}}}
	var renderer Generic
	var row model1.Row
	require.NoError(t, renderer.Render(obj, "", &row))
	assert.Equal(t, "Ready: False", row.Fields[2])
	disabled := false
	renderer.SetViewSetting(&config.ViewSetting{Readiness: &disabled})
	require.NoError(t, renderer.Render(obj, "", &row))
	assert.Empty(t, row.Fields[2])
}

func TestTableReadinessKeepsPrinterCellOrder(t *testing.T) {
	var renderer Table
	renderer.SetViewSetting(&config.ViewSetting{Columns: []string{"VALID", "NAME", "AGE"}})
	table := &metav1.Table{ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Age"}, {Name: "Ready"}, {Name: "Name"}}, Rows: []metav1.TableRow{{Cells: []any{"1m", "False", "sample"}}}}
	renderer.SetTable("test", table)
	var row model1.Row
	require.NoError(t, renderer.Render(table.Rows[0], "test", &row))
	assert.Equal(t, "test/sample", row.ID)
	assert.Equal(t, model1.Fields{"READY: False", "sample", "1m", "False"}, row.Fields)
	// A subsequent table without Age must not reuse its previous age index.
	table = &metav1.Table{ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Name"}, {Name: "Ready"}}, Rows: []metav1.TableRow{{Cells: []any{"sample", "True"}}}}
	renderer.SetViewSetting(nil)
	renderer.SetTable("test", table)
	require.NoError(t, renderer.Render(table.Rows[0], "test", &row))
	assert.Equal(t, model1.Fields{"sample", "True", ""}, row.Fields)
}
