// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func quotaObject(t *testing.T, hard, used map[corev1.ResourceName]string) *unstructured.Unstructured {
	t.Helper()
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "budget", Namespace: "test"}, Status: corev1.ResourceQuotaStatus{Hard: quantities(hard), Used: quantities(used)}}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(quota)
	require.NoError(t, err)
	return &unstructured.Unstructured{Object: obj}
}

func quantities(values map[corev1.ResourceName]string) corev1.ResourceList {
	out := make(corev1.ResourceList, len(values))
	for key, value := range values {
		out[key] = resource.MustParse(value)
	}
	return out
}

func TestResourceQuota(t *testing.T) {
	for _, tc := range []struct {
		name          string
		hard, used    string
		want, percent string
		severity      config.SeverityLevel
	}{
		{"normal", "10", "4", "4/10 (40%)", "40%", config.SeverityLow},
		{"warn", "10", "7", "7/10 (70%)", "70%", config.SeverityMedium},
		{"critical", "10", "9", "9/10 (90%)", "90%", config.SeverityHigh},
		{"over", "10", "12", "12/10 (120%)", "120%", config.SeverityHigh},
		{"blocked", "0", "0", "0/0 (blocked)", "", config.SeverityLow},
		{"over zero", "0", "1", "1/0 (∞)", "∞", config.SeverityHigh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := quotaObject(t, map[corev1.ResourceName]string{corev1.ResourcePods: tc.hard}, map[corev1.ResourceName]string{corev1.ResourcePods: tc.used})
			renderer := ResourceQuota{}
			var row model1.Row
			require.NoError(t, renderer.Render(obj, "", &row))
			assert.Equal(t, "test/budget", row.ID)
			assert.Len(t, row.Fields, len(renderer.Header("")))
			assert.Equal(t, tc.want, row.Fields[6])
			assert.Equal(t, tc.percent, row.Fields[7])
			assert.Equal(t, tc.severity, row.Severity)
			assert.Equal(t, tc.severity != config.SeverityHigh, model1.IsValid("", renderer.Header(""), row))
		})
	}
}

func TestQuotaAllResourcesAndThresholds(t *testing.T) {
	obj := quotaObject(t, map[corev1.ResourceName]string{corev1.ResourceRequestsCPU: "2", corev1.ResourceRequestsMemory: "1Gi", corev1.ResourceRequestsStorage: "10Gi"}, map[corev1.ResourceName]string{corev1.ResourceRequestsCPU: "1500m", corev1.ResourceRequestsMemory: "512Mi", corev1.ResourceRequestsStorage: "9Gi"})
	renderer := ResourceQuota{Thresholds: config.Threshold{config.CPU: &config.Severity{Warn: 60, Critical: 75}}}
	var row model1.Row
	require.NoError(t, renderer.Render(obj, "", &row))
	assert.Equal(t, "1500m/2 (75%)", row.Fields[2])
	assert.Equal(t, "512Mi/1Gi (50%)", row.Fields[3])
	assert.Equal(t, "90%", row.Fields[7])
	assert.Equal(t, "requests.cpu 75%; requests.storage 90%", row.Fields[8])
	assert.Equal(t, config.SeverityHigh, row.Severity)
	renderer.SetViewSetting(&config.ViewSetting{Columns: []string{"NAME"}})
	require.NoError(t, renderer.Render(obj, "", &row))
	projected := row.Customize([]int{0})
	assert.Equal(t, config.SeverityHigh, projected.Severity)
	oldErr, oldPending := model1.ErrColor, model1.PendingColor
	model1.ErrColor, model1.PendingColor = tcell.ColorRed, tcell.ColorYellow
	t.Cleanup(func() { model1.ErrColor, model1.PendingColor = oldErr, oldPending })
	assert.Equal(t, tcell.ColorRed, renderer.ColorerFunc()("", model1.Header{{Name: "NAME"}}, &model1.RowEvent{Row: projected}))
	projected.Severity = config.SeverityMedium
	assert.Equal(t, tcell.ColorYellow, renderer.ColorerFunc()("", model1.Header{{Name: "NAME"}}, &model1.RowEvent{Row: projected}))
}

func TestQuotaMissingUsageAndLegacyAliases(t *testing.T) {
	obj := quotaObject(t, map[corev1.ResourceName]string{corev1.ResourceCPU: "2", corev1.ResourceLimitsMemory: "1Gi"}, map[corev1.ResourceName]string{corev1.ResourceCPU: "1"})
	var row model1.Row
	renderer := ResourceQuota{}
	require.NoError(t, renderer.Render(obj, "", &row))
	assert.Equal(t, "1/2 (50%)", row.Fields[2])
	assert.Equal(t, "-/1Gi", row.Fields[5])
	assert.Empty(t, row.Fields[6])
	assert.Equal(t, "50%", row.Fields[7])
	obj = quotaObject(t, nil, nil)
	obj.Object["spec"] = map[string]any{"hard": map[string]any{"pods": "2"}}
	require.NoError(t, renderer.Render(obj, "", &row))
	assert.Equal(t, "-/2", row.Fields[6])
	assert.Empty(t, row.Fields[7])
}

func TestLimitRangePreservesItemsAndIdentity(t *testing.T) {
	limits := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: "limits", Namespace: "test"}, Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{
		{Type: corev1.LimitTypeContainer, Default: quantities(map[corev1.ResourceName]string{corev1.ResourceCPU: "1", corev1.ResourceMemory: "512Mi"}), DefaultRequest: quantities(map[corev1.ResourceName]string{corev1.ResourceCPU: "100m"}), Min: quantities(map[corev1.ResourceName]string{corev1.ResourceCPU: "10m"}), Max: quantities(map[corev1.ResourceName]string{corev1.ResourceCPU: "2"})},
		{Type: corev1.LimitTypePod, Min: quantities(map[corev1.ResourceName]string{corev1.ResourceCPU: "50m"}), Max: quantities(map[corev1.ResourceName]string{corev1.ResourceMemory: "1Gi"})},
	}}}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(limits)
	require.NoError(t, err)
	var row model1.Row
	renderer := LimitRange{}
	require.NoError(t, renderer.Render(&unstructured.Unstructured{Object: obj}, "", &row))
	assert.Equal(t, "test/limits", row.ID)
	assert.Equal(t, model1.Fields{"test", "limits", "Container, Pod", "Container=1", "Container=512Mi", "Container=100m", "", "Container=10m; Pod=50m", "", "Container=2", "Pod=1Gi"}, row.Fields[:len(row.Fields)-1])
	assert.Len(t, row.Fields, len(renderer.Header("")))
	renderer.SetViewSetting(&config.ViewSetting{Columns: []string{"NAME", "CPU(MIN)"}})
	require.NoError(t, renderer.Render(&unstructured.Unstructured{Object: obj}, "", &row))
	assert.Equal(t, "limits", row.Fields[0])
	assert.Equal(t, "Container=10m; Pod=50m", row.Fields[1])
	assert.Equal(t, "test/limits", row.ID)
}

func TestQuotaPercentageSort(t *testing.T) {
	header := (ResourceQuota{}).Header("")
	idx, ok := header.IndexOf("MAX%", true)
	require.True(t, ok)
	assert.False(t, header[idx].MX, "quota usage must remain visible without metrics-server")
	events := model1.NewRowEvents(4)
	for _, value := range []string{"100%", "9%", "∞", "90%"} {
		row := model1.NewRow(len(header))
		row.ID = value
		row.Fields[idx] = value
		events.Add(&model1.RowEvent{Row: row})
	}
	table := model1.NewTableDataWithRows(client.RqGVR, header, events)
	table.Sort(model1.SortColumn{Name: "MAX%", ASC: true})
	var got []string
	table.RowsRange(func(_ int, event model1.RowEvent) bool { got = append(got, event.Row.Fields[idx]); return true })
	assert.Equal(t, []string{"9%", "90%", "100%", "∞"}, got)
}

func TestQuotaExactPercent(t *testing.T) {
	assert.Equal(t, 58.0, quotaPercent(resource.MustParse("580m"), resource.MustParse("1")))
	renderer := ResourceQuota{Thresholds: config.Threshold{config.CPU: &config.Severity{Warn: 50, Critical: 58}}}
	obj := quotaObject(t, map[corev1.ResourceName]string{corev1.ResourceRequestsCPU: "1"}, map[corev1.ResourceName]string{corev1.ResourceRequestsCPU: "580m"})
	var row model1.Row
	require.NoError(t, renderer.Render(obj, "", &row))
	assert.Equal(t, "580m/1 (58%)", row.Fields[2])
	assert.Equal(t, config.SeverityHigh, row.Severity)
}
