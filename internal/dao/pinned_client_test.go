// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/dynamic"
)

type switchingActionConn struct {
	client.Connection
	dynamic dynamic.Interface
}

func (c *switchingActionConn) DynDial() (dynamic.Interface, error) { return c.dynamic, nil }

func TestPinnedActionClient(t *testing.T) {
	t.Run("bulk-state", func(t *testing.T) {
		original := fakecluster.New(t, fakecluster.WithObjects(mutCronJob("a", nil), mutCronJob("b", nil)))
		other := fakecluster.New(t, fakecluster.WithObjects(mutCronJob("a", nil), mutCronJob("b", nil)))
		conn := &switchingActionConn{Connection: original.Conn(), dynamic: original.Dyn()}
		var resource CronJob
		resource.Init(mutFactory(t, conn), client.CjGVR)
		require.NoError(t, resource.PinClient())
		require.NoError(t, resource.SetSuspend(context.Background(), "ns1/a", true))
		conn.dynamic = other.Dyn()
		require.NoError(t, resource.SetSuspend(context.Background(), "ns1/b", true))
		assert.Len(t, original.Actions("patch", "cronjobs"), 2)
		assert.Empty(t, other.Actions("patch", "cronjobs"))
	})
	t.Run("finalizer-delete", func(t *testing.T) {
		a, b := finalizerObject(), finalizerObject()
		a.SetName("a")
		b.SetName("b")
		original := fakecluster.New(t, fakecluster.WithObjects(a, b))
		other := fakecluster.New(t, fakecluster.WithObjects(a.DeepCopy(), b.DeepCopy()))
		conn := &switchingActionConn{Connection: original.Conn(), dynamic: original.Dyn()}
		var resource Generic
		resource.Init(mutFactory(t, conn), client.CmGVR)
		require.NoError(t, resource.PinClient())
		require.NoError(t, resource.DeleteWithFinalizers(context.Background(), "ns1/a", "original", nil, DefaultGrace, false))
		conn.dynamic = other.Dyn()
		require.NoError(t, resource.DeleteWithFinalizers(context.Background(), "ns1/b", "original", nil, DefaultGrace, false))
		assert.Len(t, original.Actions("patch", "configmaps"), 2)
		assert.Len(t, original.Actions("delete", "configmaps"), 2)
		assert.Empty(t, other.Actions("patch", "configmaps"))
		assert.Empty(t, other.Actions("delete", "configmaps"))
	})
}
