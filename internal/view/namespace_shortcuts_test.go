// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"slices"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/globlit/k9s/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNamespaceShortcutLimit(t *testing.T) {
	for _, listNamespaces := range []bool{true, false} {
		t.Run(map[bool]string{true: "with all", false: "without all"}[listNamespaces], func(t *testing.T) {
			favorites := []string{"all", "one", "two", "three", "four", "five", "six", "seven"}
			fc := fakecluster.New(t, fakecluster.WithNamespaces(favorites[1:]...), fakecluster.WithRBAC(func(_ string, gvr *client.GVR, _, verb string) bool {
				return listNamespaces || gvr != client.NsGVR || verb != "list"
			}))
			cfg := config.NewConfig(fc.Conn().Config())
			cfg.SetConnection(fc.Conn())
			ct, err := cfg.K9s.ActivateContext(fc.Conn().ActiveContext())
			require.NoError(t, err)
			ct.Namespace.Favorites = slices.Clone(favorites)
			b := NewBrowser(client.PodGVR).(*Browser)
			b.app = NewApp(cfg)
			b.meta = &metav1.APIResource{Namespaced: true}
			actions := ui.NewKeyActions()
			b.namespaceActions(actions)
			assert.Len(t, b.namespaces, 5)
			for i, key := range ui.NumKeys {
				_, bound := actions.Get(key)
				assert.Equal(t, i < 5, bound, "namespace key %d", i)
			}
			if listNamespaces {
				assert.Equal(t, client.NamespaceAll, b.namespaces[0])
				assert.Equal(t, "four", b.namespaces[4])
			} else {
				assert.Equal(t, "one", b.namespaces[0])
				assert.Equal(t, "five", b.namespaces[4])
			}
			assert.Equal(t, favorites, cfg.FavNamespaces())
		})
	}
}
