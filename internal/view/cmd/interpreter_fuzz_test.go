// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"maps"
	"strings"
	"testing"
)

var interpreterSeeds = []string{
	"",
	" ",
	"pod",
	"pod fred",
	"pod      fred   ",
	"DEPLOY Fred",
	"pod app=blee fred @zorg",
	"pod 'app in (MyApp,Other)' @Fred",
	"pod app=MyApp,Tier=Backend",
	"pod =blee",
	"pod /cilium kube-system",
	"pod -f fred",
	"pod -ffred",
	"pod -f",
	"POD /FRED",
	"pod fred=blee,zorg=duh ns1",
	"pod 'fred in (blee,zorg),blee notin (zorg)'",
	"pod -l app.kubernetes.io/name=fred",
	"pod -lfred=blee",
	"pod -l 'fred in (blee,zorg)'",
	"pod ns1 /fred app=blee,fred=zorg",
	"pod /!fred",
	"pod 'unmatched",
	"xray po",
	"xray po default",
	"can u:fernand",
	"can   u:  fernand  ",
	"ctx @Dev",
	"ctx Dev",
	"dir /tmp",
	"ls ~/fred /blee",
	"cow moo",
	"q!",
	"ns kube-system",
	"pod app!=blee",
	"pod app==blee",
}

func checkInterpreter(p *Interpreter) {
	p.HasNS()
	p.Args()
	p.IsBlank()
	p.IsCowCmd()
	p.IsHelpCmd()
	p.IsBailCmd()
	p.IsAliasCmd()
	p.IsXrayCmd()
	p.IsContextCmd()
	p.IsNamespaceCmd()
	p.IsDirCmd()
	p.IsRBACCmd()
	p.ContextArg()
	p.DirArg()
	p.CowArg()
	p.RBACArgs()
	p.XrayArgs()
	p.FilterArg()
	p.FuzzyArg()
	p.NSArg()
	p.HasContext()
	_, _ = p.LabelsSelector()
}

// setArgs drops empty values: the accessors treat them as unset.
func setArgs(a args) map[string]string {
	m := make(map[string]string, len(a))
	for k, v := range a {
		if v != "" {
			m[k] = v
		}
	}

	return m
}

func isSpecialCmd(p *Interpreter) bool {
	return p.IsCowCmd() || p.IsBailCmd() || p.IsHelpCmd() || p.IsAliasCmd() ||
		p.IsXrayCmd() || p.IsRBACCmd() || p.IsContextCmd() || p.IsNamespaceCmd() ||
		p.IsDirCmd()
}

func FuzzInterpreter(f *testing.F) {
	for _, s := range interpreterSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		p := NewInterpreter(s)
		checkInterpreter(p)

		p1 := NewInterpreter(p.GetLine())
		if p1.Cmd() != p.Cmd() {
			t.Fatalf("cmd: got %q, want %q", p1.Cmd(), p.Cmd())
		}
		if got, want := setArgs(p1.args), setArgs(p.args); !maps.Equal(got, want) {
			t.Fatalf("args: got %#v, want %#v", got, want)
		}

		p.SwitchNS("fred")
		checkInterpreter(p)
		p.ClearNS()
		checkInterpreter(p)
	})
}

// FuzzInterpreterMerge checks the line that Merge builds, as Command.run does.
func FuzzInterpreterMerge(f *testing.F) {
	for _, s := range interpreterSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		p := NewInterpreter(s)
		if p.IsBlank() || isSpecialCmd(p) {
			return
		}
		want := setArgs(p.args)
		// Skip values with a stray quote: only malformed quotes make them.
		for _, v := range want {
			if strings.Contains(v, "'") {
				return
			}
		}
		p.Merge(NewInterpreter(s))
		checkInterpreter(p)

		p1 := NewInterpreter(p.GetLine())
		if p1.Cmd() != p.Cmd() {
			t.Fatalf("cmd: got %q, want %q", p1.Cmd(), p.Cmd())
		}
		if got := setArgs(p1.args); !maps.Equal(got, want) {
			t.Fatalf("line %q: args: got %#v, want %#v", p.GetLine(), got, want)
		}
	})
}
