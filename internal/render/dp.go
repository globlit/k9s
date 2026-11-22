// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/model1"
	"github.com/globlit/tcell/v2"
	"github.com/globlit/tview"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

var defaultDPHeader = model1.Header{
	model1.HeaderColumn{Name: colNamespace},
	model1.HeaderColumn{Name: colName},
	model1.HeaderColumn{Name: colVS, Attrs: model1.Attrs{VS: true}},
	model1.HeaderColumn{Name: colReady, Attrs: model1.Attrs{Align: tview.AlignRight}},
	model1.HeaderColumn{Name: "UP-TO-DATE", Attrs: model1.Attrs{Align: tview.AlignRight}},
	model1.HeaderColumn{Name: "AVAILABLE", Attrs: model1.Attrs{Align: tview.AlignRight}},
	model1.HeaderColumn{Name: colRollout},
	model1.HeaderColumn{Name: colLabels, Attrs: model1.Attrs{Wide: true}},
	model1.HeaderColumn{Name: colValid, Attrs: model1.Attrs{Wide: true}},
	model1.HeaderColumn{Name: colAge, Attrs: model1.Attrs{Time: true}},
}

// Deployment renders a K8s Deployment to screen.
type Deployment struct {
	Base
}

// ColorerFunc colors a resource row.
func (Deployment) ColorerFunc() model1.ColorerFunc {
	return func(ns string, h model1.Header, re *model1.RowEvent) tcell.Color {
		c := model1.DefaultColorer(ns, h, re)
		if !model1.IsValid(ns, h, re.Row) {
			return c
		}

		idx, ok := h.IndexOf("READY", true)
		if !ok {
			return c
		}
		ready := strings.TrimSpace(re.Row.Fields[idx])
		tt := strings.Split(ready, "/")
		if len(tt) == 2 && tt[1] == "0" {
			return model1.PendingColor
		}

		return c
	}
}

// Header returns a header row.
func (d Deployment) Header(_ string) model1.Header {
	return d.doHeader(defaultDPHeader)
}

// Render renders a K8s resource to screen.
func (d Deployment) Render(o any, _ string, row *model1.Row) error {
	raw, ok := o.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected Unstructured, but got %T", o)
	}
	if err := d.defaultRow(raw, row); err != nil {
		return err
	}
	if d.specs.isEmpty() {
		return nil
	}
	cols, err := d.specs.realize(raw, defaultDPHeader, row)
	cols.hydrateRow(row)

	return err
}

// defaultRow populates the row fields with Deployment data.
func (d Deployment) defaultRow(raw *unstructured.Unstructured, r *model1.Row) error {
	var dp appsv1.Deployment
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &dp)
	if err != nil {
		return err
	}

	desired := desiredReplicas(dp.Spec.Replicas)
	r.ID = client.MetaFQN(&dp.ObjectMeta)
	r.Fields = model1.Fields{
		dp.Namespace,
		dp.Name,
		computeVulScore(dp.Namespace, dp.Labels, &dp.Spec.Template.Spec),
		strconv.Itoa(int(dp.Status.AvailableReplicas)) + "/" + strconv.Itoa(int(desired)),
		strconv.Itoa(int(dp.Status.UpdatedReplicas)),
		strconv.Itoa(int(dp.Status.AvailableReplicas)),
		deploymentRollout(&dp),
		mapToStr(dp.Labels),
		AsStatus(d.diagnose(&dp)),
		ToAge(dp.GetCreationTimestamp()),
	}
	stashAge(r, dp.GetCreationTimestamp())

	return nil
}

func (Deployment) diagnose(dp *appsv1.Deployment) error {
	if err := rolloutError(deploymentRollout(dp)); err != nil {
		return err
	}
	desired, current, avail := desiredReplicas(dp.Spec.Replicas), dp.Status.Replicas, dp.Status.AvailableReplicas
	if current != avail {
		return fmt.Errorf("desiring %d replicas got %d available", current, avail)
	}
	if desired != avail {
		return fmt.Errorf("want %d replicas got %d available", desired, avail)
	}

	return nil
}
