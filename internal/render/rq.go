// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/tcell/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

var defaultRQHeader = model1.Header{
	{Name: colNamespace}, {Name: colName},
	{Name: "CPU(R)"}, {Name: "MEM(R)"}, {Name: "CPU(L)"}, {Name: "MEM(L)"}, {Name: "PODS"},
	{Name: "MAX%"}, {Name: colValid, Attrs: model1.Attrs{Wide: true}}, {Name: colAge, Attrs: model1.Attrs{Time: true}},
}

// ResourceQuota renders usage across every quota dimension, including resources
// not shown in the standard columns. Thresholds are an immutable model snapshot.
type ResourceQuota struct {
	Base
	Thresholds config.Threshold
}

func (q ResourceQuota) Header(string) model1.Header { return q.doHeader(defaultRQHeader) }

func (ResourceQuota) ColorerFunc() model1.ColorerFunc {
	return func(ns string, h model1.Header, re *model1.RowEvent) tcell.Color {
		switch re.Row.Severity {
		case config.SeverityHigh:
			return model1.ErrColor
		case config.SeverityMedium:
			return model1.PendingColor
		}
		return model1.DefaultColorer(ns, h, re)
	}
}

func (q ResourceQuota) Render(o any, _ string, row *model1.Row) error {
	raw, ok := o.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected Unstructured, but got %T", o)
	}
	var quota corev1.ResourceQuota
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &quota); err != nil {
		return err
	}
	hard := quota.Status.Hard
	if len(hard) == 0 {
		hard = quota.Spec.Hard
	}
	cells := make(map[corev1.ResourceName]string, len(hard))
	maxPercent := -1.0
	var failures []string
	row.Severity = config.SeverityLow
	for name, limit := range hard {
		used, known := quota.Status.Used[name]
		if !known {
			cells[name] = "-/" + limit.String()
			continue
		}
		percent := quotaPercent(used, limit)
		suffix := quotaPercentText(percent)
		if limit.IsZero() && used.IsZero() {
			suffix = "blocked"
		}
		cells[name] = used.String() + "/" + limit.String() + " (" + suffix + ")"
		maxPercent = max(maxPercent, percent)
		level := q.levelFor(name, percent)
		row.Severity = max(row.Severity, level)
		if level == config.SeverityHigh {
			failures = append(failures, string(name)+" "+suffix)
		}
	}
	sort.Strings(failures)
	row.ID = client.MetaFQN(&quota.ObjectMeta)
	row.Fields = model1.Fields{quota.Namespace, quota.Name}
	for _, name := range []corev1.ResourceName{corev1.ResourceRequestsCPU, corev1.ResourceRequestsMemory, corev1.ResourceLimitsCPU, corev1.ResourceLimitsMemory, corev1.ResourcePods} {
		value := cells[name]
		// cpu/memory are legacy aliases for requests.cpu/requests.memory.
		if value == "" && name == corev1.ResourceRequestsCPU {
			value = cells[corev1.ResourceCPU]
		}
		if value == "" && name == corev1.ResourceRequestsMemory {
			value = cells[corev1.ResourceMemory]
		}
		row.Fields = append(row.Fields, value)
	}
	row.Fields = append(row.Fields, quotaPercentText(maxPercent), strings.Join(failures, "; "), ToAge(quota.CreationTimestamp))
	stashAge(row, quota.CreationTimestamp)
	if q.specs.isEmpty() {
		return nil
	}
	cols, err := q.specs.realize(raw, defaultRQHeader, row)
	cols.hydrateRow(row)
	return err
}

func quotaPercent(used, hard resource.Quantity) float64 {
	if hard.Sign() <= 0 {
		if used.Sign() > 0 {
			return math.Inf(1)
		}
		// A 0/0 quota forbids use but does not represent exhausted allocation.
		return -1
	}
	// Quantities are decimal rationals. Float division can turn 580m/1 into
	// 57.999... and miss an exact threshold boundary.
	usedRatio, _ := new(big.Rat).SetString(used.AsDec().String())
	hardRatio, _ := new(big.Rat).SetString(hard.AsDec().String())
	ratio := new(big.Rat).Quo(usedRatio, hardRatio)
	ratio.Mul(ratio, big.NewRat(100, 1))
	whole := new(big.Int).Quo(ratio.Num(), ratio.Denom())
	percent, _ := new(big.Float).SetInt(whole).Float64()
	return math.Max(0, percent)
}

func quotaPercentText(percent float64) string {
	if percent < 0 {
		return ""
	}
	if math.IsInf(percent, 1) {
		return "∞"
	}
	return fmt.Sprintf("%.0f%%", math.Floor(percent))
}

func (q ResourceQuota) levelFor(name corev1.ResourceName, percent float64) config.SeverityLevel {
	if percent < 0 {
		return config.SeverityLow
	}
	key := string(name)
	switch name {
	case corev1.ResourceCPU, corev1.ResourceRequestsCPU, corev1.ResourceLimitsCPU:
		key = config.CPU
	case corev1.ResourceMemory, corev1.ResourceRequestsMemory, corev1.ResourceLimitsMemory:
		key = config.MEM
	}
	thresholds := q.Thresholds
	if thresholds[key] == nil {
		thresholds = config.Threshold{key: config.NewSeverity()}
	}
	// LevelFor accepts percentages only up to 100. Keep the real value for display.
	return thresholds.LevelFor(key, int(math.Min(100, percent)))
}
