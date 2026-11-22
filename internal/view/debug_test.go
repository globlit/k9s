// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/config/mock"
	"github.com/globlit/k9s/internal/ui"
	"github.com/stretchr/testify/assert"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func TestBuildDebugArgs(t *testing.T) {
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig, flags.Context, flags.BearerToken = new("/tmp/kube config"), new("isolated"), new("test-token")
	args := buildDebugArgs("team-a/web", "app", "k9s-debug-test", config.DebugContainer{Image: "registry/debug:1", Profile: "general"}, flags)
	assert.Equal(t, []string{"debug", "-it", "-n", "team-a", "web", "--token", "test-token", "-c", "k9s-debug-test", "--image=registry/debug:1", "--target=app", "--profile=general", "--", "sh"}, args)
	assert.Equal(t, []string{"debug", "-it", "-n", "team-a", "web", "-c", "debug", "--image=busybox:1.37.0", "--target=app", "--profile=general", "--", "sh"}, buildDebugArgs("team-a/web", "app", "debug", config.NewDebugContainer(), nil))
}

func TestDebugBindingsRespectReadOnly(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		cfg := mock.NewMockConfig(t)
		cfg.K9s.ReadOnly = readOnly
		v := &cronJobViewerStub{app: NewApp(cfg)}
		for _, bind := range []func(*ui.KeyActions){(&Pod{ResourceViewer: v}).bindKeys, (&Container{ResourceViewer: v}).bindKeys} {
			aa := ui.NewKeyActions()
			bind(aa)
			action, exists := aa.Get(ui.KeyShiftD)
			assert.Equal(t, !readOnly, exists)
			if exists {
				assert.Equal(t, "Debug", action.Description)
				assert.True(t, action.Opts.Dangerous)
			}
		}
	}
}
