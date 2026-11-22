// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model1_test

import (
	"fmt"
	"testing"

	"github.com/globlit/k9s/internal/client"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/model1"
	"github.com/stretchr/testify/assert"
)

func TestRowSeveritySurvivesProjection(t *testing.T) {
	row := model1.Row{ID: "test/quota", Fields: model1.Fields{"quota", "app=test"}, Severity: config.SeverityHigh}
	assert.Equal(t, config.SeverityHigh, row.Clone().Severity)
	assert.Equal(t, config.SeverityHigh, row.Customize([]int{0}).Severity)
	assert.Equal(t, config.SeverityHigh, row.Labelize([]int{0}, 1, []string{"app"}).Severity)
	changed := row.Clone()
	changed.Severity = config.SeverityMedium
	assert.True(t, row.Diff(changed, -1))
	assert.False(t, row.Diff(row.Clone(), -1))
}

func TestQuotaFaultFilterAfterProjection(t *testing.T) {
	events := model1.NewRowEvents(3)
	for i, level := range []config.SeverityLevel{config.SeverityLow, config.SeverityMedium, config.SeverityHigh} {
		row := model1.Row{ID: fmt.Sprint(i), Fields: model1.Fields{fmt.Sprint(i)}, Severity: level}
		events.Add(&model1.RowEvent{Row: row})
	}
	table := model1.NewTableDataWithRows(client.RqGVR, model1.Header{{Name: "NAME"}}, events)
	filtered := table.Filter(model1.FilterOpts{Toast: true})
	assert.Equal(t, 1, filtered.RowCount())
	row, ok := filtered.RowAt(0)
	assert.True(t, ok)
	assert.Equal(t, "2", row.Row.ID)
}

func TestTableSeverityChangeWithoutTextDelta(t *testing.T) {
	table := model1.NewTableData(client.RqGVR)
	table.SetHeader("test", model1.Header{{Name: "NAME"}})
	row := model1.Row{ID: "test/quota", Fields: model1.Fields{"quota"}}
	table.Update(model1.Rows{row})
	before := table.Clone()
	row.Severity = config.SeverityHigh
	table.Update(model1.Rows{row})
	assert.True(t, table.Diff(before))
	assert.Equal(t, 1, table.Filter(model1.FilterOpts{Toast: true}).RowCount())
}
