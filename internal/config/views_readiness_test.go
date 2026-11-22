// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config_test

import (
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/config/data"
	configjson "github.com/globlit/k9s/internal/config/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestReadinessOnlyView(t *testing.T) {
	input := []byte("views:\n  example.com/v1/widgets:\n    readiness: false\n")
	require.NoError(t, data.JSONValidator.Validate(configjson.ViewsSchema, input))
	var views config.CustomView
	require.NoError(t, yaml.Unmarshal(input, &views))
	setting := views.Views["example.com/v1/widgets"]
	assert.False(t, setting.IsBlank())
	assert.False(t, setting.HasCols(), "readiness must not request full objects")
	assert.False(t, setting.ReadinessEnabled())
	assert.False(t, setting.Equals(&config.ViewSetting{}))
	disabled := false
	assert.True(t, setting.Equals(&config.ViewSetting{Readiness: &disabled}))
	var absent *config.ViewSetting
	assert.True(t, absent.ReadinessEnabled())
}
