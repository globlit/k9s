// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package model_test

import (
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/dao"
	"github.com/globlit/k9s/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogJSONRefreshAndFilter(t *testing.T) {
	logs := model.NewLog(client.PodGVR, makeLogOpts(10), time.Millisecond)
	observer := newTestView()
	logs.AddListener(observer)
	source := `time {"level":"error","msg":"raw [red]"}`
	logs.Append(dao.NewLogItemFromString(source))
	logs.Append(dao.NewLogItemFromString(`time {"level":"info","msg":"other"}`))
	logs.Notify()
	require.Len(t, observer.data, 2)
	original := string(observer.data[0])
	logs.SetJSON(true)
	require.Len(t, observer.data, 2)
	assert.Contains(t, string(observer.data[0]), `level=[-::-][indianred::]"error"`)
	logs.Filter("-j level=error")
	require.Len(t, observer.data, 1)
	assert.Contains(t, string(observer.data[0]), "raw [red[]")
	palette := config.NewJSONLog()
	palette.LevelError = "maroon"
	logs.SetJSONPalette(palette)
	require.Len(t, observer.data, 1)
	assert.Contains(t, string(observer.data[0]), `[maroon::]"error"`)
	logs.SetJSON(false)
	require.Len(t, observer.data, 1)
	assert.Equal(t, original, string(observer.data[0]), "toggling must preserve retained source")
	logs.Filter("-j level=missing")
	assert.Empty(t, observer.data, "a field selector with zero matches must not show all logs")
	assert.Zero(t, observer.errCalled)
}
