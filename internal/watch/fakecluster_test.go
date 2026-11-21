// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package watch_test

import (
	"context"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/watch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func TestFactoryListsFakeClusterPods(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.MirrorTypedToDyn(), fakecluster.WithObjects(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p1"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p2"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns2", Name: "p3"}},
	))
	f := watch.NewFactory(fc.Conn())
	f.Start("ns1")
	t.Cleanup(f.Terminate)

	oo, err := f.List(client.PodGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)
	assert.Len(t, oo, 2)

	oo, err = f.List(client.PodGVR, client.NamespaceAll, true, labels.Everything())
	require.NoError(t, err)
	assert.Len(t, oo, 3)
	assert.Positive(t, fc.CanICalls())

	cs, _ := fc.Conn().Dial()
	_, err = cs.CoreV1().Pods("ns1").Create(context.Background(),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "p4"}}, metav1.CreateOptions{})
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		oo, err := f.List(client.PodGVR, "ns1", false, labels.Everything())
		return err == nil && len(oo) == 3
	}, 5*time.Second, 10*time.Millisecond)
}

func TestFactoryHonorsFakeClusterRBAC(t *testing.T) {
	fc := fakecluster.New(t, fakecluster.DenyNS("ns1"))
	f := watch.NewFactory(fc.Conn())
	f.Start("ns1")
	t.Cleanup(f.Terminate)

	_, err := f.List(client.PodGVR, "ns1", true, labels.Everything())
	require.Error(t, err)
}
