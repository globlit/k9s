// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"context"
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newFakeFactory(t *testing.T, fc *fakecluster.Cluster) *watch.Factory {
	t.Helper()
	f := watch.NewFactory(fc.Conn())
	t.Cleanup(f.Terminate)

	return f
}

func TestWorkloadDeleteWithoutGVR(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}},
	))
	var w Workload
	w.Init(newFakeFactory(t, fc), client.WkGVR)

	var err error
	require.NotPanics(t, func() {
		err = w.Delete(context.Background(), "ns1/p1", nil, DefaultGrace)
	})
	require.Error(t, err)
	assert.Empty(t, fc.Deletes())
}

func TestWorkloadDelete(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}},
	))
	var w Workload
	w.Init(newFakeFactory(t, fc), client.WkGVR)

	ctx := context.WithValue(context.Background(), internal.KeyGVR, client.PodGVR)
	require.NoError(t, w.Delete(ctx, "ns1/p1", nil, DefaultGrace))
	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, "p1", dd[0].Name)
}
