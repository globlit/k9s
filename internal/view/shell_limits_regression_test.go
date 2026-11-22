// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/testutil/fakecluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestShellLimitsThroughConfigLoader(t *testing.T) {
	for name, tc := range map[string]struct {
		limits      string
		schemaError bool
		wantError   string
	}{
		"valid":            {`{cpu: 100m, memory: 64Mi}`, false, ""},
		"missing_cpu":      {`{memory: 64Mi}`, true, "missing shell pod cpu limit"},
		"missing_memory":   {`{cpu: 100m}`, true, "missing shell pod memory limit"},
		"empty_cpu":        {`{cpu: "", memory: 64Mi}`, false, "missing shell pod cpu limit"},
		"empty_memory":     {`{cpu: 100m, memory: ""}`, false, "missing shell pod memory limit"},
		"malformed_cpu":    {`{cpu: not-a-quantity, memory: 64Mi}`, false, "invalid shell pod cpu limit"},
		"malformed_memory": {`{cpu: 100m, memory: bad}`, false, "invalid shell pod memory limit"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := "k9s:\n  shellPod:\n    image: busybox:1.36.1\n    namespace: default\n    limits: " + tc.limits + "\n"
			require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			cfg := config.NewConfig(nil)
			err := cfg.Load(path, false)
			if tc.schemaError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			cfg.K9s.ShellPod.Validate()
			pod, err := k9sShellPod("n1", cfg.K9s.ShellPod)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, pod)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "100m", pod.Spec.Containers[0].Resources.Limits.Cpu().String())
			assert.Equal(t, "64Mi", pod.Spec.Containers[0].Resources.Limits.Memory().String())
		})
	}
}

func TestShellLimitsDefaults(t *testing.T) {
	for _, limits := range []config.Limits{nil, {}} {
		cfg := &config.ShellPod{Limits: limits}
		cfg.Validate()
		pod, err := k9sShellPod("n1", cfg)
		require.NoError(t, err)
		assert.Equal(t, "100m", pod.Spec.Containers[0].Resources.Limits.Cpu().String())
		assert.Equal(t, "100Mi", pod.Spec.Containers[0].Resources.Limits.Memory().String())
	}
}

func TestShellInvalidLimitsDoNotCreatePod(t *testing.T) {
	fc := fakecluster.New(t)
	s := shellApp(t, fc)
	s.onLoop(func() {
		s.app.Config.K9s.ShellPod.Limits = config.Limits{corev1.ResourceCPU: "invalid", corev1.ResourceMemory: "100Mi"}
	})
	err := launchShellPod(context.Background(), s.app, "n1")
	require.ErrorContains(t, err, "invalid shell pod cpu limit")
	assert.Empty(t, fc.Actions("create", "pods"))
}
