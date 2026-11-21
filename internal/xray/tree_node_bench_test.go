// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package xray_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/xray"
)

func benchTree(deps, pods int) *xray.TreeNode {
	root := xray.NewTreeNode(client.NewGVR("root"), "root")
	ns := xray.NewTreeNode(client.NsGVR, "default")
	root.Add(ns)
	for d := range deps {
		dp := xray.NewTreeNode(client.DpGVR, fmt.Sprintf("default/dp-%d", d))
		ns.Add(dp)
		for p := range pods {
			po := xray.NewTreeNode(client.PodGVR, fmt.Sprintf("default/dp-%d-po-%d", d, p))
			dp.Add(po)
			po.Add(xray.NewTreeNode(client.CoGVR, fmt.Sprintf("c-%d", p%3)))
		}
	}

	return root
}

func BenchmarkTreeNodeFilter(b *testing.B) {
	root := benchTree(100, 10)
	match := func(q, path string) bool { return strings.Contains(path, q) }

	b.ReportAllocs()
	for b.Loop() {
		root.Filter("dp-4", match)
	}
}
