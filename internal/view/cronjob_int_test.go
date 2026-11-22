// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"testing"

	"github.com/globlit/k9s/internal/config/mock"
	"github.com/globlit/k9s/internal/ui"
	"github.com/stretchr/testify/assert"
)

type cronJobViewerStub struct {
	ResourceViewer

	app *App
}

func (s *cronJobViewerStub) App() *App {
	return s.app
}

func TestCronJobBindKeysReadOnly(t *testing.T) {
	uu := map[string]struct {
		readOnly bool
	}{
		"read-write": {readOnly: false},
		"read-only":  {readOnly: true},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			cfg := mock.NewMockConfig(t)
			cfg.K9s.ReadOnly = u.readOnly
			c := CronJob{ResourceViewer: &cronJobViewerStub{app: NewApp(cfg)}}

			aa := ui.NewKeyActions()
			c.bindKeys(aa)

			_, hasTrigger := aa.Get(ui.KeyT)
			_, hasSuspend := aa.Get(ui.KeyS)
			assert.Equal(t, !u.readOnly, hasTrigger)
			assert.Equal(t, !u.readOnly, hasSuspend)
		})
	}
}

func TestDeployPauseBinding(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		cfg := mock.NewMockConfig(t)
		cfg.K9s.ReadOnly = readOnly
		v := &cronJobViewerStub{app: NewApp(cfg)}
		d := Deploy{ResourceViewer: v}
		aa := ui.NewKeyActions()
		aa.Add(ui.KeyShiftP, ui.NewKeyAction("Sort Namespace", nil, false))
		d.bindKeys(aa)
		action, _ := aa.Get(ui.KeyShiftP)
		expected := "Pause/Resume"
		if readOnly {
			expected = "Sort Namespace"
		}
		assert.Equal(t, expected, action.Description)
		assert.Equal(t, !readOnly, action.Opts.Dangerous)
	}
}
