// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package health_test

import (
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/health"
	"github.com/stretchr/testify/assert"
)

func TestCheckSet(t *testing.T) {
	c := health.NewCheck(client.PodGVR)
	c.Set(health.S1, 5)
	c.Inc(health.S1)
	c.Total(10)

	assert.Equal(t, int64(6), c.Tally(health.S1))
	assert.Equal(t, int64(10), c.Tally(health.Corpus))
	assert.Zero(t, c.Tally(health.Unknown))
	assert.Nil(t, c.GetObjectKind())
	assert.Equal(t, *c, c.DeepCopyObject())
}
