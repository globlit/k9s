// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

// Package testutil holds shared test helpers.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/globlit/k9s/internal/config"
)

// Main runs the tests with config, data, state, cache and kube files in a
// temp dir. Call it from TestMain.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := Isolate()
	if dir != "" {
		defer os.RemoveAll(dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil:", err)
		return 2
	}

	return m.Run()
}

// Isolate points the k9s, XDG and kube env vars at a new temp dir and
// reloads the config locations. It returns the temp dir.
func Isolate() (string, error) {
	known := realKubeconfigs()
	dir, err := os.MkdirTemp("", "k9s-test-")
	if err != nil {
		return "", err
	}
	kcfg := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kcfg, nil, 0o600); err != nil {
		return dir, err
	}
	for k, v := range map[string]string{
		config.K9sEnvConfigDir: filepath.Join(dir, "k9s"),
		config.K9sEnvLogsDir:   filepath.Join(dir, "logs"),
		"XDG_CONFIG_HOME":      filepath.Join(dir, "config"),
		"XDG_DATA_HOME":        filepath.Join(dir, "data"),
		"XDG_STATE_HOME":       filepath.Join(dir, "state"),
		"XDG_CACHE_HOME":       filepath.Join(dir, "cache"),
		"KUBECACHEDIR":         filepath.Join(dir, "kubecache"),
		"KUBECONFIG":           kcfg,
	} {
		if err := os.Setenv(k, v); err != nil {
			return dir, err
		}
	}
	if err := refuseReal(known); err != nil {
		return dir, err
	}
	xdg.Reload()
	if err := config.InitLocs(); err != nil {
		return dir, err
	}

	return dir, nil
}

func realKubeconfigs() []string {
	var pp []string
	if home, err := os.UserHomeDir(); err == nil {
		pp = append(pp, filepath.Join(home, ".kube", "config"))
	}

	return append(pp, filepath.SplitList(os.Getenv("KUBECONFIG"))...)
}

func refuseReal(known []string) error {
	seen := make(map[string]struct{}, len(known))
	for _, p := range known {
		seen[canonical(p)] = struct{}{}
	}
	for _, p := range filepath.SplitList(os.Getenv("KUBECONFIG")) {
		if _, ok := seen[canonical(p)]; ok {
			return fmt.Errorf("KUBECONFIG points at a real kubeconfig (%s): refusing to run", p)
		}
	}

	return nil
}

func canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}

	return p
}
