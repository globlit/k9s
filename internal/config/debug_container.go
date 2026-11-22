// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

import "strings"

// DebugContainer holds defaults for ephemeral debug containers.
type DebugContainer struct {
	Image   string `json:"image" yaml:"image"`
	Profile string `json:"profile" yaml:"profile"`
}

func NewDebugContainer() DebugContainer {
	return DebugContainer{Image: defaultDockerShellImage, Profile: "general"}
}

// Validate returns defaults for missing fields without changing shared config.
func (d DebugContainer) Validate() DebugContainer {
	d.Image, d.Profile = strings.TrimSpace(d.Image), strings.TrimSpace(d.Profile)
	if d.Image == "" {
		d.Image = defaultDockerShellImage
	}
	if d.Profile == "" {
		d.Profile = "general"
	}
	return d
}
