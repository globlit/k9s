// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"regexp"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

func mutCronJob(n string, suspend *bool) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: n, UID: types.UID(n + "-uid")},
		Spec: batchv1.CronJobSpec{
			Schedule: "* * * * *",
			Suspend:  suspend,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{"app": "a"},
					Annotations: map[string]string{"note": "x"},
				},
				Spec: batchv1.JobSpec{BackoffLimit: new(int32(2))},
			},
		},
	}
}

func newCronJob(t *testing.T, conn client.Connection) *CronJob {
	t.Helper()
	var c CronJob
	c.Init(mutFactory(t, conn), client.CjGVR)

	return &c
}

func TestCronJobRun(t *testing.T) {
	long := strings.Repeat("x", 50)
	uu := map[string]struct {
		name, prefix string
	}{
		"short": {name: "cj1", prefix: "cj1"},
		"long":  {name: long, prefix: long[:maxJobNameSize]},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, fakecluster.WithObjects(mutCronJob(u.name, nil)))

			require.NoError(t, newCronJob(t, fc.Conn()).Run("ns1/"+u.name))

			cc := fc.Actions("create", "jobs")
			require.Len(t, cc, 1)
			job := cc[0].(k8stesting.CreateAction).GetObject().(*batchv1.Job)
			assert.Regexp(t, regexp.MustCompile(`\A`+u.prefix+`-manual-[a-z0-9]{3}\z`), job.Name)
			assert.Equal(t, "ns1", job.Namespace)
			assert.Equal(t, map[string]string{"app": "a"}, job.Labels)
			assert.Equal(t, map[string]string{"note": "x"}, job.Annotations)
			assert.Equal(t, int32(2), *job.Spec.BackoffLimit)
			assert.Equal(t, []metav1.OwnerReference{{
				APIVersion:         "batch/v1",
				Kind:               "CronJob",
				Name:               u.name,
				UID:                types.UID(u.name + "-uid"),
				Controller:         new(true),
				BlockOwnerDeletion: new(true),
			}}, job.OwnerReferences)
		})
	}
}

func TestCronJobRunErrors(t *testing.T) {
	uu := map[string]struct {
		path string
		fc   []fakecluster.Option
		conn func(client.Connection) client.Connection
		err  string
	}{
		"deny-create": {path: "ns1/cj1", fc: []fakecluster.Option{fakecluster.DenyVerb("create")}, err: "not authorized to run jobs"},
		"missing":     {path: "ns1/nope", err: "not found"},
		"create":      {path: "ns1/cj1", fc: []fakecluster.Option{fakecluster.FailOn("create", "jobs", 0, errMut)}, err: "boom"},
		"cani": {
			path: "ns1/cj1",
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, canIErr: errMut} },
			err:  "boom",
		},
		"dial": {
			path: "ns1/cj1",
			conn: func(c client.Connection) client.Connection { return &mutConn{Connection: c, dialErr: errMut} },
			err:  "boom",
		},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			fc := fakecluster.New(t, append([]fakecluster.Option{fakecluster.WithObjects(mutCronJob("cj1", nil))}, u.fc...)...)
			var conn client.Connection = fc.Conn()
			if u.conn != nil {
				conn = u.conn(conn)
			}

			require.ErrorContains(t, newCronJob(t, conn).Run(u.path), u.err)
			if k != "create" {
				assert.Empty(t, fc.Actions("create", "jobs"))
			}
		})
	}
}
