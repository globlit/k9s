// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/testutil/expect"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/tcell/v2"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// e2eDebounce is longer than the command buffer key entry delay.
const e2eDebounce = 250 * time.Millisecond

// e2eAge returns a creation time d ago. Ages in days render the same all day.
func e2eAge(days int) metav1.Time {
	return metav1.NewTime(time.Now().Add(-time.Duration(days)*24*time.Hour - time.Minute))
}

func e2eMeta(ns, n string, days int, ll map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: n, Labels: ll, CreationTimestamp: e2eAge(days), UID: types.UID("uid-" + ns + "-" + n)}
}

func e2ePod(ns, n string, days int, ll map[string]string, cc ...string) *corev1.Pod {
	if len(cc) == 0 {
		cc = []string{"main"}
	}
	po := corev1.Pod{
		ObjectMeta: e2eMeta(ns, n, days, ll),
		Spec:       corev1.PodSpec{NodeName: "node-1"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.1"},
	}
	for _, c := range cc {
		po.Spec.Containers = append(po.Spec.Containers, corev1.Container{Name: c, Image: "nginx:1.27"})
		po.Status.ContainerStatuses = append(po.Status.ContainerStatuses, corev1.ContainerStatus{
			Name:  c,
			Image: "nginx:1.27",
			Ready: true,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: e2eAge(days)}},
		})
	}

	return &po
}

func e2eObjects() []runtime.Object {
	web := map[string]string{"app": "web"}
	one := int32(1)
	two := int32(2)
	rs := appsv1.ReplicaSet{
		ObjectMeta: e2eMeta("ns-a", "web-7d9", 10, web),
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &two,
			Selector: &metav1.LabelSelector{MatchLabels: web},
		},
		Status: appsv1.ReplicaSetStatus{Replicas: 2, ReadyReplicas: 2},
	}
	rs.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "uid-ns-a-web"}}
	web1 := e2ePod("ns-a", "web-7d9-aaaaa", 10, web, "nginx", "sidecar")
	web2 := e2ePod("ns-a", "web-7d9-bbbbb", 9, web, "nginx", "sidecar")
	for _, p := range []*corev1.Pod{web1, web2} {
		p.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-7d9", UID: "uid-ns-a-web-7d9"}}
	}

	return []runtime.Object{
		web1, web2, &rs,
		e2ePod("ns-a", "api-0", 3, map[string]string{"app": "api"}),
		e2ePod("ns-b", "db-0", 30, map[string]string{"app": "db"}),
		e2ePod("ns-b", "cache-0", 5, map[string]string{"app": "cache"}),
		&appsv1.Deployment{
			ObjectMeta: e2eMeta("ns-a", "web", 10, web),
			Spec: appsv1.DeploymentSpec{
				Replicas: &two,
				Selector: &metav1.LabelSelector{MatchLabels: web},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: web},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "nginx", Image: "nginx:1.27"}},
						Volumes: []corev1.Volume{{
							Name: "cfg",
							VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "web-cfg"},
							}},
						}},
					},
				},
			},
			Status: appsv1.DeploymentStatus{Replicas: 2, ReadyReplicas: 2, AvailableReplicas: 2, UpdatedReplicas: 2},
		},
		&appsv1.StatefulSet{
			ObjectMeta: e2eMeta("ns-b", "db", 30, map[string]string{"app": "db"}),
			Spec:       appsv1.StatefulSetSpec{Replicas: &one, ServiceName: "db"},
			Status:     appsv1.StatefulSetStatus{Replicas: 1, ReadyReplicas: 1},
		},
		&appsv1.DaemonSet{ObjectMeta: e2eMeta("ns-b", "agent", 30, nil)},
		&corev1.Service{
			ObjectMeta: e2eMeta("ns-a", "web-svc", 10, nil),
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.96.0.10",
				Selector:  web,
				Ports:     []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}},
			},
		},
		&batchv1.CronJob{
			ObjectMeta: e2eMeta("ns-b", "backup", 30, nil),
			Spec:       batchv1.CronJobSpec{Schedule: "0 1 * * *"},
		},
		&batchv1.Job{ObjectMeta: e2eMeta("ns-b", "backup-1", 1, nil)},
		&corev1.ConfigMap{ObjectMeta: e2eMeta("ns-a", "web-cfg", 10, nil), Data: map[string]string{"mode": "prod"}},
		&corev1.Secret{ObjectMeta: e2eMeta("ns-a", "web-tls", 10, nil), Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"key": []byte("s3cr3t")}},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1", CreationTimestamp: e2eAge(100), Labels: map[string]string{"node-role.kubernetes.io/worker": ""}},
			Status: corev1.NodeStatus{
				Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				NodeInfo:    corev1.NodeSystemInfo{KubeletVersion: "v1.37.2"},
				Addresses:   []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "172.18.0.2"}},
				Capacity:    corev1.ResourceList{},
				Allocatable: corev1.ResourceList{},
			},
		},
	}
}

// e2eCluster builds the shared e2e fake cluster with two namespaces.
func e2eCluster(t testing.TB, opts ...fakecluster.Option) *fakecluster.Cluster {
	t.Helper()
	oo := append([]fakecluster.Option{
		fakecluster.WithObjects(e2eObjects()...),
	}, opts...)

	return fakecluster.New(t, oo...)
}

var e2eTimeRX = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z`)

// e2eMask returns the screen text without the flash row and timestamps,
// which change with time.
func e2eMask(scr string) string {
	ll := strings.Split(strings.TrimRight(scr, "\n"), "\n")
	if len(ll) > 0 {
		ll = ll[:len(ll)-1]
	}

	return e2eTimeRX.ReplaceAllString(strings.Join(ll, "\n"), "<time>") + "\n"
}

// e2eWait waits until the screen contains all ss.
func (s *SimApp) e2eWait(ss ...string) {
	s.t.Helper()
	s.Eventually(func() bool {
		scr := s.Screen()
		for _, x := range ss {
			if !strings.Contains(scr, x) {
				return false
			}
		}
		return true
	}, "screen does not show "+strings.Join(ss, ", "))
}

// e2eGone waits until the screen contains none of ss.
func (s *SimApp) e2eGone(ss ...string) {
	s.t.Helper()
	s.Eventually(func() bool {
		scr := s.Screen()
		for _, x := range ss {
			if strings.Contains(scr, x) {
				return false
			}
		}
		return true
	}, "screen still shows "+strings.Join(ss, ", "))
}

// e2eMatch waits for the screen to show testdata/expected/name, then
// compares it. The wait skips short redraw states.
func (s *SimApp) e2eMatch(name string) {
	s.t.Helper()
	want, err := os.ReadFile(filepath.Join(expect.Dir, name))
	deadline := time.Now().Add(simTimeout)
	got := e2eMask(s.Screen())
	for err == nil && got != string(want) && time.Now().Before(deadline) {
		s.Sync()
		got = e2eMask(s.Screen())
	}
	expect.Match(s.t, name, got)
}

// e2eSettle waits until the command buffer debounce has run.
func (s *SimApp) e2eSettle() {
	s.t.Helper()
	t0 := time.Now()
	s.Eventually(func() bool { return time.Since(t0) > e2eDebounce }, "settle")
}

// e2eFilter types a filter, presses Enter and waits for the debounce.
func (s *SimApp) e2eFilter(f string) {
	s.t.Helper()
	s.Keys('/', f, tcell.KeyEnter)
	s.e2eSettle()
}
