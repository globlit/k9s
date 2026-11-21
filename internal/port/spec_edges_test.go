// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package port_test

import (
	"context"
	"net"
	"testing"

	"github.com/globlit/k9s/internal/port"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
)

func specs() port.ContainerPortSpecs {
	return port.FromContainerPorts("c1", []v1.ContainerPort{
		{Name: "http", ContainerPort: 80, Protocol: v1.ProtocolTCP},
		{ContainerPort: 9090, Protocol: v1.ProtocolTCP},
		{Name: "dns", ContainerPort: 53, Protocol: v1.ProtocolUDP},
	})
}

func TestFromContainerPortsSkipsNonTCP(t *testing.T) {
	ss := specs()
	require.Len(t, ss, 2)
	assert.Equal(t, "c1::80(http)\nc1::9090", ss.Dump())
	p0, p1 := ss[0].Port(), ss[1].Port()
	assert.Equal(t, "http", p0.String())
	assert.Equal(t, "9090", p1.String())
}

func TestContainerPortSpecsMatchSpec(t *testing.T) {
	uu := map[string]struct {
		ss port.ContainerPortSpecs
		s  string
		e  bool
	}{
		"no-specs":   {s: "c1::1", e: true},
		"plain-port": {ss: specs(), s: "8080", e: true},
		"match":      {ss: specs(), s: "c1::80", e: true},
		"by-num":     {ss: specs(), s: "c1::9090", e: true},
		"wrong-co":   {ss: specs(), s: "c2::80"},
		"wrong-port": {ss: specs(), s: "c1::81"},
		"udp":        {ss: specs(), s: "c1::53"},
		"empty-co":   {ss: specs(), s: "::80"},
	}
	for k, u := range uu {
		t.Run(k, func(t *testing.T) {
			assert.Equal(t, u.e, u.ss.MatchSpec(u.s))
		})
	}
	assert.False(t, port.ContainerPortSpec{Container: "c1"}.MatchSpec("c1"))
}

func TestContainerPortSpecsFind(t *testing.T) {
	ss := specs()

	pf, err := port.ParsePF("c1::http")
	require.NoError(t, err)
	spec, ok := ss.Find(pf)
	require.True(t, ok)
	assert.Equal(t, "80", spec.PortNum)

	pf, err = port.ParsePF("c1::9090")
	require.NoError(t, err)
	spec, ok = ss.Find(pf)
	require.True(t, ok)
	assert.Equal(t, "9090", spec.PortNum)

	pf, err = port.ParsePF("c2::http")
	require.NoError(t, err)
	_, ok = ss.Find(pf)
	assert.False(t, ok)
}

func TestContainerPortSpecToPFAnn(t *testing.T) {
	ss := specs()
	a := ss[0].ToPFAnn()
	assert.Equal(t, "c1", a.Container)
	assert.Equal(t, "http", a.ContainerPort.String())
	assert.Equal(t, "80", a.LocalPort)
	assert.True(t, ss[0].Match(a))
}

func TestContainerPortSpecsToTunnels(t *testing.T) {
	tt := specs().ToTunnels("localhost")
	require.Len(t, tt, 2)
	assert.Equal(t, port.PortTunnel{Address: "localhost", LocalPort: "80", ContainerPort: "80"}, tt[0])
	assert.Equal(t, "9090:9090", tt[1].PortMap())
}

func TestPortTunnel(t *testing.T) {
	pt := port.NewPortTunnel("localhost", "c1", "", "80")
	assert.Equal(t, "localhost|c1|:80", pt.String())
	assert.Equal(t, "80:80", pt.PortMap(), "use the container port when no local port is set")
	pt.LocalPort = "8080"
	assert.Equal(t, "8080:80", pt.PortMap())
}

func TestIsPortFree(t *testing.T) {
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	_, busy, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)

	ctx := context.Background()
	assert.False(t, port.IsPortFree(ctx, port.NewPortTunnel("127.0.0.1", "c1", busy, "80")))
	assert.True(t, port.IsPortFree(ctx, port.NewPortTunnel("127.0.0.1", "c1", "0", "80")))

	tt := port.PortTunnels{
		port.NewPortTunnel("127.0.0.1", "c1", "0", "80"),
		port.NewPortTunnel("127.0.0.1", "c1", busy, "80"),
	}
	require.ErrorContains(t, tt.CheckAvailable(ctx), busy)
	require.NoError(t, tt[:1].CheckAvailable(ctx))
}

func TestPFAnnsToTunnelsUnavailable(t *testing.T) {
	aa, err := port.ParsePFs("c1::8080:80,c1::9090:90")
	require.NoError(t, err)

	none := func(context.Context, port.PortTunnel) bool { return false }
	pts, err := aa.ToTunnels("localhost", nil, none)
	require.Error(t, err)
	assert.Empty(t, pts)

	all := func(context.Context, port.PortTunnel) bool { return true }
	pts, err = aa.ToTunnels("localhost", nil, all)
	require.NoError(t, err)
	assert.Len(t, pts, 2)
}

func TestParsePFsInvalid(t *testing.T) {
	for _, s := range []string{"", "::80", "c1::80,", "a b", "c1::80:x:y"} {
		_, err := port.ParsePFs(s)
		assert.Error(t, err, s)
	}
	assert.Nil(t, specs().MatchAnnotations("a b"))

	pf, err := port.ParsePF("http")
	require.NoError(t, err, "read a bare word as a port name")
	assert.Equal(t, "http", pf.ContainerPort.StrVal)
	assert.Empty(t, pf.LocalPort)
}

func TestToTunnelsMismatch(t *testing.T) {
	_, err := port.ToTunnels("localhost", "c1::80,c1::90", "8080")
	require.ErrorContains(t, err, "mismatch")
	_, err = port.ToTunnels("localhost", "bad", "8080")
	require.Error(t, err)
}
