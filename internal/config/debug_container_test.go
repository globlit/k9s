// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDebugContainerDefaults(t *testing.T) {
	assert.Equal(t, NewDebugContainer(), (DebugContainer{}).Validate())
	d := DebugContainer{Image: " custom:v1 ", Profile: " restricted "}
	assert.Equal(t, DebugContainer{Image: "custom:v1", Profile: "restricted"}, d.Validate())
	assert.Equal(t, " custom:v1 ", d.Image)
}
