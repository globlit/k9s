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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func phasePod(n string, ph corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: n},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c1"}}},
		Status:     corev1.PodStatus{Phase: ph},
	}
}

func TestPodSanitizeIsGraceful(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.WithObjects(
		phasePod("pending", corev1.PodPending),
		phasePod("running", corev1.PodRunning),
	))
	f := newFakeFactory(t, fc)
	f.Start("ns1")
	_, err := f.ForResource("ns1", client.PodGVR)
	require.NoError(t, err)
	f.WaitForCacheSync()
	var p Pod
	p.Init(f, client.PodGVR)

	n, err := p.Sanitize(context.Background(), "ns1")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	dd := fc.Deletes()
	require.Len(t, dd, 1)
	assert.Equal(t, "pending", dd[0].Name)
	assert.Nil(t, dd[0].Grace)
}
