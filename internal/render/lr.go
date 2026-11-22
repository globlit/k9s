// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"fmt"
	"strings"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/model1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

var defaultLRHeader = model1.Header{
	{Name: colNamespace}, {Name: colName}, {Name: "TYPE"},
	{Name: "CPU(DEFAULT)"}, {Name: "MEM(DEFAULT)"},
	{Name: "CPU(REQUEST)"}, {Name: "MEM(REQUEST)"},
	{Name: "CPU(MIN)"}, {Name: "MEM(MIN)"},
	{Name: "CPU(MAX)"}, {Name: "MEM(MAX)"},
	{Name: colAge, Attrs: model1.Attrs{Time: true}},
}

// LimitRange keeps one row per resource so edit and delete retain real object IDs.
// Each value includes its item type, including when other item values are absent.
type LimitRange struct{ Base }

func (l LimitRange) Header(string) model1.Header { return l.doHeader(defaultLRHeader) }

func (l LimitRange) Render(o any, _ string, row *model1.Row) error {
	raw, ok := o.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected Unstructured, but got %T", o)
	}
	var limits corev1.LimitRange
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &limits); err != nil {
		return err
	}
	row.ID = client.MetaFQN(&limits.ObjectMeta)
	row.Fields = model1.Fields{limits.Namespace, limits.Name}
	types := make([]string, 0, len(limits.Spec.Limits))
	for _, item := range limits.Spec.Limits {
		types = append(types, string(item.Type))
	}
	row.Fields = append(row.Fields, strings.Join(types, ", "))
	for _, kind := range []string{"default", "request", "min", "max"} {
		for _, resource := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			values := make([]string, 0, len(limits.Spec.Limits))
			for _, item := range limits.Spec.Limits {
				var resources corev1.ResourceList
				switch kind {
				case "default":
					resources = item.Default
				case "request":
					resources = item.DefaultRequest
				case "min":
					resources = item.Min
				case "max":
					resources = item.Max
				}
				if quantity, exists := resources[resource]; exists {
					values = append(values, string(item.Type)+"="+quantity.String())
				}
			}
			row.Fields = append(row.Fields, strings.Join(values, "; "))
		}
	}
	row.Fields = append(row.Fields, ToAge(limits.CreationTimestamp))
	stashAge(row, limits.CreationTimestamp)
	if l.specs.isEmpty() {
		return nil
	}
	cols, err := l.specs.realize(raw, defaultLRHeader, row)
	cols.hydrateRow(row)
	return err
}
