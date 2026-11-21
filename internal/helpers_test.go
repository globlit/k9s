// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package internal_test

import (
	"testing"

	"github.com/globlit/k9s/internal"
	"github.com/stretchr/testify/assert"
)

func TestIsLabelSelector(t *testing.T) {
	uu := map[string]struct {
		s  string
		ok bool
	}{
		"empty":       {s: ""},
		"cool":        {s: "-l app=fred,env=blee", ok: true},
		"no-flag":     {s: "app=fred,env=blee", ok: true},
		"no-space":    {s: "-lapp=fred,env=blee", ok: true},
		"wrong-flag":  {s: "-f app=fred,env=blee"},
		"missing-key": {s: "=fred"},
		"missing-val": {s: "fred="},
	}

	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.ok, internal.IsLabelSelector(u.s))
		})
	}
}

func TestIsLabelSelectorMore(t *testing.T) {
	uu := map[string]struct {
		s  string
		ok bool
	}{
		"flag-only":  {s: "-l", ok: true},
		"not-eq":     {s: "app!=fred", ok: true},
		"double-eq":  {s: "app==fred", ok: true},
		"spaced":     {s: "app = fred"},
		"plain":      {s: "fred"},
		"mixed-good": {s: "app=fred,blee", ok: true},
		"commas":     {s: ",,,"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.ok, internal.IsLabelSelector(u.s))
		})
	}
}

func TestIsInverseSelector(t *testing.T) {
	uu := map[string]struct {
		s  string
		ok bool
	}{
		"empty":    {s: ""},
		"plain":    {s: "fred"},
		"inverse":  {s: "!fred", ok: true},
		"bang":     {s: "!", ok: true},
		"inner":    {s: "fr!ed"},
		"space":    {s: " !fred"},
		"labelish": {s: "!app=fred", ok: true},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.ok, internal.IsInverseSelector(u.s))
		})
	}
}

func TestIsFuzzySelector(t *testing.T) {
	uu := map[string]struct {
		s, q string
		ok   bool
	}{
		"empty":      {s: ""},
		"plain":      {s: "fred"},
		"space":      {s: "-f fred", q: "fred", ok: true},
		"no-space":   {s: "-ffred", q: "fred", ok: true},
		"dash":       {s: "-f fred-blee", q: "fred-blee", ok: true},
		"under":      {s: "-f fred_1", q: "fred_1", ok: true},
		"trailing":   {s: "-f fred blee", q: "fred", ok: true},
		"dot-stops":  {s: "-f fred.blee", q: "fred", ok: true},
		"two-spaces": {s: "-f  fred"},
		"no-term":    {s: "-f"},
		"not-prefix": {s: "x -f fred"},
		"label-flag": {s: "-l app=fred"},
		"only-punct": {s: "-f !!"},
	}

	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			q, ok := internal.IsFuzzySelector(u.s)
			assert.Equal(t, u.ok, ok)
			assert.Equal(t, u.q, q)
		})
	}
}
