// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package watch

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/globlit/k9s/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/labels"
)

func TestFactoryJanitorEvictsIdleNamespaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := NewFactory(dynConn{dial: newRSDial()})
		require.NoError(t, f.SetActiveNS("ns2"))
		require.NoError(t, f.SetActiveNS("ns1"))
		f.Start("ns1")

		time.Sleep(factoryIdleTTL - factoryJanitorTick/2)
		synctest.Wait()
		assert.NotNil(t, f.FactoryFor("ns2"), "evict only after the TTL")

		time.Sleep(factoryJanitorTick)
		synctest.Wait()
		assert.Nil(t, f.FactoryFor("ns2"))
		assert.NotNil(t, f.FactoryFor("ns1"), "keep the active namespace")

		f.Terminate()
		synctest.Wait()
	})
}

func TestFactoryJanitorKeepsUsedNamespaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := NewFactory(dynConn{dial: newRSDial()})
		require.NoError(t, f.SetActiveNS("ns1"))
		f.Start("ns1")

		for range 3 {
			_, err := f.ensureFactory("ns2")
			require.NoError(t, err)
			time.Sleep(factoryIdleTTL / 2)
			synctest.Wait()
		}
		assert.NotNil(t, f.FactoryFor("ns2"))

		time.Sleep(factoryIdleTTL + factoryJanitorTick)
		synctest.Wait()
		assert.Nil(t, f.FactoryFor("ns2"))

		f.Terminate()
		synctest.Wait()
	})
}

func TestFactoryEvictIdleClusterWideClearsCache(t *testing.T) {
	f := NewFactory(dynConn{dial: newRSDial(newRS("ns1", "rs1"))})
	f.Start(client.NamespaceAll)
	defer f.Terminate()

	_, err := f.List(client.RsGVR, client.NamespaceAll, true, labels.Everything())
	require.NoError(t, err)
	require.NoError(t, f.SetActiveNS("ns1"))
	f.mx.RLock()
	n := len(f.cwInfs)
	f.mx.RUnlock()
	assert.Equal(t, 1, n)

	f.evictIdle(time.Now().Add(2*factoryIdleTTL), factoryIdleTTL)
	assert.Nil(t, f.FactoryFor(client.BlankNamespace))
	f.mx.RLock()
	n = len(f.cwInfs)
	f.mx.RUnlock()
	assert.Zero(t, n)
}

func TestStripCRDSchemas(t *testing.T) {
	crd := newRS("", "crd1")
	crd.Object["spec"] = map[string]any{
		"versions": []any{
			map[string]any{"name": "v1", "schema": map[string]any{"a": 1}},
			"bad",
		},
	}
	o, err := stripCRDSchemas(crd)
	require.NoError(t, err)
	assert.Same(t, crd, o)
	assert.Empty(t, crd.GetManagedFields())
	v := crd.Object["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)
	assert.NotContains(t, v, "schema")
	assert.Equal(t, "v1", v["name"])

	o, err = stripCRDSchemas("not an object")
	require.NoError(t, err)
	assert.Equal(t, "not an object", o)
}

func TestToGVR(t *testing.T) {
	assert.Equal(t, client.PodGVR.GVR(), toGVR("v1/pods"))
	assert.Equal(t, client.DpGVR.GVR(), toGVR("apps/v1/deployments"))
}

func TestDebugFactory(t *testing.T) {
	f := NewFactory(dynConn{dial: newRSDial(newRS("ns1", "rs1"))})
	f.Start("ns1")
	defer f.Terminate()

	_, err := f.List(client.RsGVR, "ns1", true, labels.Everything())
	require.NoError(t, err)
	DumpFactory(f)
	DebugFactory(f, "ns1", "apps/v1/replicasets")
	DebugFactory(f, "missing", "apps/v1/replicasets")
}
