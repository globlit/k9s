// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/config/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestStructuredLoggerConfig(t *testing.T) {
	raw := []byte("k9s:\n  logger:\n    json: true\n    jsonFields: [level, msg, error]\n")
	require.NoError(t, json.NewValidator().Validate(json.K9sSchema, raw))
	var wrapper struct {
		K9s struct {
			Logger config.Logger `yaml:"logger"`
		} `yaml:"k9s"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &wrapper))
	assert.True(t, wrapper.K9s.Logger.JSON)
	assert.Equal(t, []string{"level", "msg", "error"}, wrapper.K9s.Logger.Validate().JSONFields)
	assert.False(t, config.NewLogger().JSON)
}

func TestStructuredSkinOverlay(t *testing.T) {
	raw := []byte("k9s:\n  views:\n    logs:\n      json:\n        key: blue\n    diff:\n      add: green\n")
	require.NoError(t, json.NewValidator().Validate(json.SkinSchema, raw))
	path := filepath.Join(t.TempDir(), "skin.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	styles := config.NewStyles()
	require.NoError(t, styles.Load(path, false))
	assert.Equal(t, config.Color("blue").String(), styles.Views().Log.JSON.Key.String())
	assert.NotEmpty(t, styles.Views().Log.JSON.String)
	assert.Equal(t, config.Color("green").String(), styles.Views().Diff.Add.String())
	original := styles.Views()
	styles.K9s.Views.Invert()
	assert.Equal(t, original.Log.JSON.Key.InvertColor(), styles.Views().Log.JSON.Key)
	assert.Equal(t, original.Diff.Add.InvertColor(), styles.Views().Diff.Add)
}

// Old skins inherit these colors without enabling inversion. Keep defaults away
// from near-white and near-black values so both kinds of skin remain usable.
func TestStructuredPaletteContrast(t *testing.T) {
	palette := config.NewJSONLog()
	assert.Equal(t, palette, config.NewStyles().Views().Log.JSON, "stock and fallback palettes must agree")
	diff := config.NewStyles().Views().Diff
	colors := []config.Color{palette.Key, palette.String, palette.Number, palette.Bool, palette.Null, palette.LevelError, palette.LevelWarn, palette.LevelInfo, palette.LevelDebug, diff.Add, diff.Remove, diff.Header}
	linear := func(channel int32) float64 {
		x := float64(channel) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	for _, color := range colors {
		r, g, b := color.Color().RGB()
		luminance := 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
		assert.GreaterOrEqual(t, (luminance+0.05)/0.05, 3.0, "%s on black", color)
		assert.GreaterOrEqual(t, 1.05/(luminance+0.05), 3.0, "%s on white", color)
	}
}
