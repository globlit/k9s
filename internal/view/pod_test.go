// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view_test

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config/mock"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/k9s/internal/view"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodNew(t *testing.T) {
	po := view.NewPod(client.PodGVR)

	require.NoError(t, po.Init(makeCtx(t)))
	assert.Equal(t, "Pods", po.Name())
	assert.Len(t, po.Hints(), 20)
	action, ok := po.Actions().Get(ui.KeyShiftD)
	require.True(t, ok)
	assert.Equal(t, "Debug", action.Description)
}

// Helpers...

func makeCtx(t testing.TB) context.Context {
	cfg := mock.NewMockConfig(t)
	return context.WithValue(context.Background(), internal.KeyApp, view.NewApp(cfg))
}
