// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"sync"
	"testing"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/stretchr/testify/assert"
)

func TestNonResourceAccessors(t *testing.T) {
	var n NonResource
	n.Init(nil, client.HmGVR)
	n.SetIncludeObject(true)

	assert.Equal(t, client.HmGVR.String(), n.gvrStr())
	assert.Nil(t, n.getFactory())
	assert.True(t, n.includeObj)
	_, err := n.Get(t.Context(), "fred")
	assert.Error(t, err)
}

func TestNonResourceGVRConcurrentInit(t *testing.T) {
	var n NonResource
	n.Init(nil, client.PodGVR)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for range 10_000 {
					_ = n.GVR()
				}
			})
			wg.Go(func() {
				for range 10_000 {
					n.Init(nil, client.PodGVR)
				}
			})
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("GVR and Init deadlocked")
	}
}
