// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/model"
	"github.com/globlit/k9s/internal/ui/dialog"
	"github.com/globlit/tcell/v2"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

func (p *Pod) debugCmd(evt *tcell.EventKey) *tcell.EventKey {
	path := p.GetTable().GetSelectedItem()
	if path == "" {
		return evt
	}
	showDebug(p.App(), p, path, "")
	return nil
}

func (c *Container) debugCmd(evt *tcell.EventKey) *tcell.EventKey {
	co := c.GetTable().GetSelectedItem()
	if co == "" {
		return evt
	}
	showDebug(c.App(), c, c.GetTable().Path, co)
	return nil
}

func showDebug(a *App, comp model.Component, path, co string) {
	if a.Config.IsReadOnly() {
		return
	}
	activeContext := a.Config.K9s.ActiveContextName()
	err := pickContainer(a, path, co, func(target string) {
		if a.Config.K9s.ActiveContextName() != activeContext {
			return
		}
		styles := a.Styles.Dialog()
		dialog.ShowDebug(&styles, a.Content.Pages, path+" / "+target, a.Config.K9s.DebugContainer, func(opts config.DebugContainer) {
			if a.Config.IsReadOnly() || a.Config.K9s.ActiveContextName() != activeContext {
				return
			}
			comp.Stop()
			defer comp.Start()
			args := buildDebugArgs(path, target, "k9s-debug-"+rand.String(8), opts, a.Conn().Config().Flags())
			if err := runK(a, &shellOpts{clear: true, args: args}); err != nil {
				a.Flash().Errf("Debug failed: %s", err)
			}
		})
	})
	if err != nil {
		a.Flash().Err(err)
	}
}

func buildDebugArgs(path, target, name string, opts config.DebugContainer, flags *genericclioptions.ConfigFlags) []string {
	// runK owns active context, kubeconfig and impersonation. Keep only the token
	// here so a startup --context cannot override the current context.
	var auth *genericclioptions.ConfigFlags
	if flags != nil {
		auth = &genericclioptions.ConfigFlags{BearerToken: flags.BearerToken}
	}
	args := buildShellArgs("debug", path, name, auth)
	return append(args, "--image="+opts.Image, "--target="+target, "--profile="+opts.Profile, "--", "sh")
}
