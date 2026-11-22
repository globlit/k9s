// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"testing"

	"github.com/globlit/k9s/internal/config/mock"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/model"
	"github.com/globlit/k9s/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogJSONBinding(t *testing.T) {
	cfg := mock.NewMockConfig(t)
	app := NewApp(cfg)
	log := &Log{app: app, logs: NewLogger(app), model: model.NewLog(nil, &dao.LogOptions{}, 0), indicator: NewLogIndicator(cfg, app.Styles, false)}
	log.bindKeys()
	action, ok := log.logs.Actions().Get(ui.KeyShiftJ)
	require.True(t, ok)
	assert.Equal(t, "Toggle JSON", action.Description)
	assert.Contains(t, log.Hints(), model.MenuHint{Mnemonic: "Shift-J", Description: "Toggle JSON", Visible: true})
	action.Action(nil)
	assert.True(t, log.indicator.JSON())
	assert.Contains(t, log.indicator.GetText(true), "JSON:On")
}
