// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"slices"
	"strings"
	"testing"

	"github.com/globlit/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var e2ePods = []string{"api-0", "cache-0", "db-0", "web-7d9-aaaaa", "web-7d9-bbbbb"}

// e2eRows returns the seeded pod names in screen order.
func (s *SimApp) e2eRows() []string {
	type pos struct {
		n string
		i int
	}
	scr := s.Screen()
	pp := make([]pos, 0, len(e2ePods))
	for _, n := range e2ePods {
		if i := strings.Index(scr, " "+n+" "); i >= 0 {
			pp = append(pp, pos{n, i})
		}
	}
	slices.SortFunc(pp, func(a, b pos) int { return a.i - b.i })
	out := make([]string, 0, len(pp))
	for _, p := range pp {
		out = append(out, p.n)
	}

	return out
}

func (s *SimApp) e2eRowsAre(nn ...string) {
	s.t.Helper()
	s.Eventually(func() bool { return slices.Equal(s.e2eRows(), nn) },
		"rows are not "+strings.Join(nn, ","))
}

func (s *SimApp) e2eSelected() []string {
	s.t.Helper()
	v, ok := s.Top().(ResourceViewer)
	require.True(s.t, ok, "top view is not a resource viewer")
	var ss []string
	s.OnLoop(func() { ss = v.GetTable().GetSelectedItems() })
	slices.Sort(ss)

	return ss
}

func TestE2EFilters(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")

	uu := []struct {
		name, filter string
		rows         []string
	}{
		{"regex", "web-.*-a", []string{"web-7d9-aaaaa"}},
		{"regex-alt", "api|db", []string{"api-0", "db-0"}},
		{"case", "API", []string{"api-0"}},
		{"namespace", "ns-b", []string{"cache-0", "db-0"}},
		{"inverse", "!web", []string{"api-0", "cache-0", "db-0"}},
		{"fuzzy", "-f cch", []string{"cache-0"}},
		{"label", "-l app=web", []string{"web-7d9-aaaaa", "web-7d9-bbbbb"}},
		{"label-set", "-l app in (api,db)", []string{"api-0", "db-0"}},
	}
	for _, u := range uu {
		t.Run(u.name, func(t *testing.T) {
			defer s.Use(t)()
			s.e2eFilter(u.filter)
			s.e2eRowsAre(u.rows...)
			s.Keys(tcell.KeyEscape)
			s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")
		})
	}

	t.Run("title", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eFilter("web")
		s.e2eWait("pods(all)[2] </web>")
		s.Keys(tcell.KeyEscape)
		s.e2eWait("pods(all)[5]")
		s.e2eGone("</web>")
	})

	// Keep all rows on an invalid regex. Only the log filter uses a literal match.
	t.Run("invalid-regex", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eFilter("web[")
		s.e2eWait("pods(all)[5] </web[>")
		s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")
		assert.Equal(t, "Pod", s.TopView())
		assert.False(t, s.DialogOpen())
		s.Keys(tcell.KeyEscape)
		s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")
	})

	t.Run("command-args", func(t *testing.T) {
		defer s.Use(t)()
		s.e2eGoto(":pod /cache", "Pod")
		s.e2eRowsAre("cache-0")
		s.e2eGoto(":pod -f wbbb", "Pod")
		s.e2eRowsAre("web-7d9-bbbbb")
		s.e2eGoto(":pod app=db", "Pod")
		s.e2eRowsAre("db-0")
		s.e2eGoto(":pod", "Pod")
		s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")
	})

	t.Run("live-typing", func(t *testing.T) {
		defer s.Use(t)()
		s.Keys('/', "db")
		s.e2eRowsAre("db-0")
		s.e2eSettle()
		s.Keys(tcell.KeyEscape)
		s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")
	})
}

func TestE2ESort(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")

	uu := []struct {
		name string
		key  rune
		rows []string
		col  string
	}{
		{"name", 'N', []string{"api-0", "cache-0", "db-0", "web-7d9-aaaaa", "web-7d9-bbbbb"}, "NAME↑"},
		{"name-desc", 'N', []string{"web-7d9-bbbbb", "web-7d9-aaaaa", "db-0", "cache-0", "api-0"}, "NAME↓"},
		{"age", 'A', []string{"api-0", "cache-0", "web-7d9-bbbbb", "web-7d9-aaaaa", "db-0"}, "AGE↑"},
		{"age-desc", 'A', []string{"db-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "api-0"}, "AGE↓"},
	}
	for _, u := range uu {
		t.Run(u.name, func(t *testing.T) {
			defer s.Use(t)()
			s.Keys(u.key)
			s.e2eRowsAre(u.rows...)
			s.e2eWait(u.col)
		})
	}
}

func TestE2EMarks(t *testing.T) {
	s := NewSimApp(t, e2eCluster(t))
	s.e2eRowsAre("api-0", "web-7d9-aaaaa", "web-7d9-bbbbb", "cache-0", "db-0")

	assert.Equal(t, []string{"ns-a/api-0"}, s.e2eSelected())

	s.Keys(' ')
	assert.Equal(t, []string{"ns-a/api-0"}, s.e2eSelected())
	s.Keys(tcell.KeyDown, tcell.KeyDown, ' ')
	assert.Equal(t, []string{"ns-a/api-0", "ns-a/web-7d9-bbbbb"}, s.e2eSelected())

	s.Keys(' ')
	assert.Equal(t, []string{"ns-a/api-0"}, s.e2eSelected())

	s.Keys(tcell.KeyDown, tcell.KeyCtrlSpace)
	assert.Equal(t, []string{"ns-a/api-0", "ns-a/web-7d9-aaaaa", "ns-a/web-7d9-bbbbb", "ns-b/cache-0"}, s.e2eSelected())

	s.Keys(tcell.KeyCtrlBackslash)
	assert.Equal(t, []string{"ns-b/cache-0"}, s.e2eSelected())
}
