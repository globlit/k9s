// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scratchKubeconfig = `apiVersion: v1
kind: Config
current-context: ct-1
clusters:
- name: cl-1
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: ct-1
  context:
    cluster: cl-1
    user: u-1
    namespace: ns-1
- name: ct-2
  context:
    cluster: cl-1
    user: u-1
users:
- name: u-1
  user:
    token: t
`

// Parse args into the root flags. Reset all flags at test end.
func parseFlags(t *testing.T, args ...string) {
	t.Helper()
	fs := rootCmd.Flags()
	timeout := k8sFlags.Timeout
	t.Cleanup(func() {
		k8sFlags.Timeout = timeout
		fs.VisitAll(func(f *pflag.Flag) {
			if s, ok := f.Value.(pflag.SliceValue); ok {
				_ = s.Replace(nil)
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
	})
	require.NoError(t, fs.Parse(args))
}

func TestRootFlagsParse(t *testing.T) {
	uu := map[string]struct {
		args []string
		get  func() any
		want any
	}{
		"refresh":      {[]string{"--refresh", "5.5"}, func() any { return *k9sFlags.RefreshRate }, float32(5.5)},
		"r":            {[]string{"-r", "1"}, func() any { return *k9sFlags.RefreshRate }, float32(1)},
		"logLevel":     {[]string{"--logLevel", "debug"}, func() any { return *k9sFlags.LogLevel }, "debug"},
		"l":            {[]string{"-l", "warn"}, func() any { return *k9sFlags.LogLevel }, "warn"},
		"logFile":      {[]string{"--logFile", "/x/k9s.log"}, func() any { return *k9sFlags.LogFile }, "/x/k9s.log"},
		"headless":     {[]string{"--headless"}, func() any { return *k9sFlags.Headless }, true},
		"logoless":     {[]string{"--logoless"}, func() any { return *k9sFlags.Logoless }, true},
		"crumbsless":   {[]string{"--crumbsless"}, func() any { return *k9sFlags.Crumbsless }, true},
		"splashless":   {[]string{"--splashless"}, func() any { return *k9sFlags.Splashless }, true},
		"invert":       {[]string{"--invert"}, func() any { return *k9sFlags.Invert }, true},
		"all-ns":       {[]string{"--all-namespaces"}, func() any { return *k9sFlags.AllNamespaces }, true},
		"A":            {[]string{"-A"}, func() any { return *k9sFlags.AllNamespaces }, true},
		"command":      {[]string{"--command", "pods"}, func() any { return *k9sFlags.Command }, "pods"},
		"c":            {[]string{"-c", "dp kube-system"}, func() any { return *k9sFlags.Command }, "dp kube-system"},
		"readonly":     {[]string{"--readonly"}, func() any { return *k9sFlags.ReadOnly }, true},
		"write":        {[]string{"--write"}, func() any { return *k9sFlags.Write }, true},
		"dump-dir":     {[]string{"--screen-dump-dir", "/d"}, func() any { return *k9sFlags.ScreenDumpDir }, "/d"},
		"kubeconfig":   {[]string{"--kubeconfig", "/k"}, func() any { return *k8sFlags.KubeConfig }, "/k"},
		"timeout":      {[]string{"--request-timeout", "3s"}, func() any { return *k8sFlags.Timeout }, "3s"},
		"context":      {[]string{"--context", "ct"}, func() any { return *k8sFlags.Context }, "ct"},
		"cluster":      {[]string{"--cluster", "cl"}, func() any { return *k8sFlags.ClusterName }, "cl"},
		"user":         {[]string{"--user", "u"}, func() any { return *k8sFlags.AuthInfoName }, "u"},
		"namespace":    {[]string{"--namespace", "ns"}, func() any { return *k8sFlags.Namespace }, "ns"},
		"n":            {[]string{"-n", "ns"}, func() any { return *k8sFlags.Namespace }, "ns"},
		"as":           {[]string{"--as", "fred"}, func() any { return *k8sFlags.Impersonate }, "fred"},
		"as-group":     {[]string{"--as-group", "g1", "--as-group", "g2"}, func() any { return *k8sFlags.ImpersonateGroup }, []string{"g1", "g2"}},
		"insecure":     {[]string{"--insecure-skip-tls-verify"}, func() any { return *k8sFlags.Insecure }, true},
		"ca":           {[]string{"--certificate-authority", "/ca"}, func() any { return *k8sFlags.CAFile }, "/ca"},
		"client-key":   {[]string{"--client-key", "/key"}, func() any { return *k8sFlags.KeyFile }, "/key"},
		"client-cert":  {[]string{"--client-certificate", "/crt"}, func() any { return *k8sFlags.CertFile }, "/crt"},
		"token":        {[]string{"--token", "tok"}, func() any { return *k8sFlags.BearerToken }, "tok"},
		"refresh-zero": {[]string{"--refresh=0"}, func() any { return *k9sFlags.RefreshRate }, float32(0)},
	}

	seen := make(map[string]struct{})
	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			parseFlags(t, u.args...)
			assert.Equal(t, u.want, u.get())
			rootCmd.Flags().Visit(func(f *pflag.Flag) { seen[f.Name] = struct{}{} })
		})
	}
	rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		_, ok := seen[f.Name]
		assert.True(t, ok, "flag %q has no parse test", f.Name)
	})
}

func TestRootFlagsDefaults(t *testing.T) {
	parseFlags(t)

	assert.InDelta(t, config.DefaultRefreshRate, *k9sFlags.RefreshRate, 0)
	assert.Equal(t, config.DefaultLogLevel, *k9sFlags.LogLevel)
	assert.Equal(t, config.AppLogFile, *k9sFlags.LogFile)
	assert.Empty(t, *k9sFlags.Command)
	assert.Empty(t, *k9sFlags.ScreenDumpDir)
	for _, b := range []*bool{
		k9sFlags.Headless, k9sFlags.Logoless, k9sFlags.Crumbsless, k9sFlags.Splashless,
		k9sFlags.Invert, k9sFlags.AllNamespaces, k9sFlags.ReadOnly, k9sFlags.Write,
	} {
		assert.False(t, *b)
	}
}

func TestRootFlagsBadValues(t *testing.T) {
	for _, args := range [][]string{
		{"--refresh", "fast"},
		{"--readonly=maybe"},
		{"--nope"},
		{"-A=2"},
	} {
		t.Run(args[0], func(t *testing.T) {
			fs := rootCmd.Flags()
			t.Cleanup(func() { parseFlags(t) })
			require.Error(t, fs.Parse(args))
		})
	}
}

func TestRootFlagsReadOnlyWrite(t *testing.T) {
	uu := map[string]struct {
		cfg  bool
		args []string
		want bool
	}{
		"none-rw":       {false, nil, false},
		"none-ro":       {true, nil, true},
		"readonly":      {false, []string{"--readonly"}, true},
		"readonly-ro":   {true, []string{"--readonly"}, true},
		"write":         {true, []string{"--write"}, false},
		"write-rw":      {false, []string{"--write"}, false},
		"both":          {false, []string{"--readonly", "--write"}, false},
		"both-reversed": {true, []string{"--write", "--readonly"}, false},
		"readonly-off":  {true, []string{"--readonly=false"}, true},
		"write-off":     {true, []string{"--write=false"}, true},
	}

	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			parseFlags(t, u.args...)
			k9s := config.NewK9s(nil, nil)
			k9s.ReadOnly = u.cfg
			k9s.Override(k9sFlags)
			assert.Equal(t, u.want, k9s.IsReadOnly())
		})
	}
}

// Load the flags into a config that uses a scratch kubeconfig.
func refineFromFlags(t *testing.T, args ...string) *config.Config {
	t.Helper()
	kcfg := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(kcfg, []byte(scratchKubeconfig), 0o600))
	parseFlags(t, append([]string{"--kubeconfig", kcfg}, args...)...)

	ks := client.NewConfig(k8sFlags)
	cfg := config.NewConfig(ks)
	cfg.K9s.Override(k9sFlags)
	require.NoError(t, cfg.Refine(k8sFlags, k9sFlags, ks))

	return cfg
}

func TestRootFlagsNamespace(t *testing.T) {
	uu := map[string]struct {
		args []string
		want string
	}{
		"context-ns":   {nil, "ns-1"},
		"n":            {[]string{"-n", "ns-2"}, "ns-2"},
		"A":            {[]string{"-A"}, client.NamespaceAll},
		"A-wins-n":     {[]string{"-n", "ns-2", "-A"}, client.NamespaceAll},
		"context-flag": {[]string{"--context", "ct-2"}, client.DefaultNamespace},
	}

	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			cfg := refineFromFlags(t, u.args...)
			assert.Equal(t, u.want, cfg.ActiveNamespace())
		})
	}
}

func TestRootFlagsContext(t *testing.T) {
	cfg := refineFromFlags(t, "--context", "ct-2")
	assert.Equal(t, "ct-2", cfg.ActiveContextName())

	kcfg := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(kcfg, []byte(scratchKubeconfig), 0o600))
	parseFlags(t, "--kubeconfig", kcfg, "--context", "nope")
	ks := client.NewConfig(k8sFlags)
	require.Error(t, config.NewConfig(ks).Refine(k8sFlags, k9sFlags, ks))
}

func TestRootFlagsCommand(t *testing.T) {
	cfg := refineFromFlags(t, "-c", "dp kube-system", "-A")
	assert.Equal(t, client.NamespaceAll, cfg.ActiveNamespace())
	assert.Equal(t, "dp kube-system", cfg.ActiveView())

	cfg.SetActiveView("svc")
	assert.Equal(t, "svc", cfg.ActiveView())
}

func TestRootFlagsAllNamespacesResetView(t *testing.T) {
	kcfg := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(kcfg, []byte(scratchKubeconfig), 0o600))
	parseFlags(t, "--kubeconfig", kcfg)
	ks := client.NewConfig(k8sFlags)
	cfg := config.NewConfig(ks)
	require.NoError(t, cfg.Refine(k8sFlags, k9sFlags, ks))
	cfg.SetActiveView("po ns-9")
	require.NoError(t, cfg.Save(true))

	cfg = refineFromFlags(t, "-A")
	assert.Equal(t, "po", cfg.ActiveView())
}

func TestRootFlagsTimeout(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		refineFromFlags(t)
		assert.Equal(t, client.DefaultCallTimeoutDuration.String(), *k8sFlags.Timeout)
	})
	t.Run("flag", func(t *testing.T) {
		refineFromFlags(t, "--request-timeout", "7s")
		assert.Equal(t, "7s", *k8sFlags.Timeout)
	})
}

func TestParseLevel(t *testing.T) {
	assert.Equal(t, "DEBUG", parseLevel("debug").String())
	assert.Equal(t, "WARN", parseLevel("warn").String())
	assert.Equal(t, "ERROR", parseLevel("error").String())
	assert.Equal(t, "INFO", parseLevel("info").String())
	assert.Equal(t, "INFO", parseLevel("DEBUG").String())
	assert.Equal(t, "INFO", parseLevel("").String())
}
