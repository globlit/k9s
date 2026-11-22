// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model

import (
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestTableThresholdSnapshot(t *testing.T) {
	table := NewTable(client.RqGVR)
	thresholds := config.NewThreshold()
	table.SetThresholds(thresholds)
	thresholds[config.CPU].Critical = 20
	delete(thresholds, config.MEM)
	assert.Equal(t, 90, table.thresholds[config.CPU].Critical)
	assert.NotNil(t, table.thresholds[config.MEM])
}
