// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package dao

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/tview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONLogRendering(t *testing.T) {
	item := NewLogItemFromString("2026-01-01T00:00:00Z " + `{"z":null,"msg":"hello [red]","level":"error","ts":"now","n":9007199254740993,"ok":true}` + "\n")
	var out bytes.Buffer
	item.Render("teal", RenderOpts{JSON: true}, &out)
	assert.Equal(t, `[slategray::]ts=[-::-][steelblue::]"now"[-::-] [slategray::]level=[-::-][indianred::]"error"[-::-] [slategray::]msg=[-::-][steelblue::]"hello [red[]"[-::-] [slategray::]n=[-::-][darkorchid::]9007199254740993[-::-] [slategray::]ok=[-::-][teal::]true[-::-] [slategray::]z=[-::-][gray::]null[-::-]`+"\n", out.String())
	out.Reset()
	item.Render("teal", RenderOpts{JSON: true, Fields: []string{"msg", "level"}}, &out)
	assert.Equal(t, `[slategray::]level=[-::-][indianred::]"error"[-::-] [slategray::]msg=[-::-][steelblue::]"hello [red[]"[-::-] [#708090::d]…+4[-::-]`+"\n", out.String())
}

func TestJSONLogFallbackAndEscaping(t *testing.T) {
	for _, line := range []string{"time not JSON [red]\n", `time {"msg":"ok"} trailing`, "time [1,2]", "time {invalid", `{"msg": bad}`} {
		item := NewLogItemFromString(line)
		var plain, structured bytes.Buffer
		item.Render("", RenderOpts{}, &plain)
		item.Render("", RenderOpts{JSON: true}, &structured)
		assert.Equal(t, plain.String(), structured.String())
	}
	raw := `{"[red]":"a\nb\t[blue]","nested":{"tag":"[green]","n":9007199254740993}}` + "\n"
	stream := make(chan *LogItem, 4)
	readLogs(context.Background(), io.NopCloser(strings.NewReader("time "+raw)), stream, &LogOptions{})
	item := <-stream
	assert.Equal(t, "time "+raw, string(item.Bytes))
	var out bytes.Buffer
	item.Render("", RenderOpts{JSON: true}, &out)
	assert.Contains(t, out.String(), `[red[]=`)
	assert.Contains(t, out.String(), `"a\nb\t[blue[]"`)
	assert.Contains(t, out.String(), `{"n":9007199254740993,"tag":"[green[]"}`)
	assert.NotContains(t, out.String(), "a\nb")
	out.Reset()
	item.Render("", RenderOpts{}, &out)
	assert.Equal(t, tview.Escape(raw), out.String())
}

func TestJSONLogFilter(t *testing.T) {
	items := NewLogItems()
	items.Add(NewLogItemFromString(`time {"level":"error","msg":"timeout, retry","n":9007199254740993,"nil":null,"ok":false}`), NewLogItemFromString(`time {"level":"info"}`), NewLogItemFromString("time error"))
	for _, q := range []string{"-j level=error", "-j level=error,n=9007199254740993", `-j "msg~timeout, retry",ok=false`, "-j nil=null", "-j level~^err"} {
		lines, matches, indices, err := items.FilterLines(0, q, RenderOpts{})
		require.NoError(t, err, q)
		assert.Len(t, lines, 3)
		assert.Equal(t, []int{0}, matches, q)
		assert.Equal(t, [][]int{nil}, indices)
	}
	for _, q := range []string{"-j absent=null", "-j level=orangered", "-j level=Error"} {
		_, matches, _, err := items.FilterLines(0, q, RenderOpts{JSON: true})
		require.NoError(t, err)
		assert.Empty(t, matches, q)
		assert.NotNil(t, matches, "zero matches must not mean no filter")
	}
	for _, q := range []string{"-j", "-j msg", "-j level~[", "-j ,", "-j =x"} {
		_, _, _, err := items.FilterLines(0, q, RenderOpts{})
		require.Error(t, err, q)
	}
}

func TestJSONLogCacheConcurrent(t *testing.T) {
	item := NewLogItemFromString(`time {"msg":"hi","n":123}`)
	assert.Nil(t, item.jsonFields)
	var plain bytes.Buffer
	item.Render("", RenderOpts{}, &plain)
	assert.Nil(t, item.jsonFields, "plain rendering must not parse JSON")
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 20 {
				var out bytes.Buffer
				item.Render("", RenderOpts{JSON: true}, &out)
				if !matchesJSON(item.parsedJSON(), []jsonPredicate{{key: "n", value: "123"}}) {
					t.Error("lost field")
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkJSONLogRender(b *testing.B) {
	raw := `time {"level":"info","msg":"request served","status":200,"duration":12.5,"nested":{"id":9007199254740993}}`
	for _, mode := range []string{"off", "warm", "cold"} {
		b.Run(mode, func(b *testing.B) {
			item := NewLogItemFromString(raw)
			opts := RenderOpts{JSON: mode != "off", Palette: config.NewJSONLog()}
			var out bytes.Buffer
			item.Render("", opts, &out)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				out.Reset()
				if mode == "cold" {
					item = NewLogItemFromString(raw)
				}
				item.Render("", opts, &out)
			}
		})
	}
}

func BenchmarkJSONLogFilter(b *testing.B) {
	for _, jsonMode := range []bool{false, true} {
		name := "off"
		if jsonMode {
			name = "on"
		}
		b.Run(name, func(b *testing.B) {
			items := NewLogItems()
			for range 500 {
				items.Add(NewLogItemFromString(`time {"level":"info","msg":"request served","status":200}`))
			}
			opts := RenderOpts{JSON: jsonMode, Palette: config.NewJSONLog()}
			_, _, _, _ = items.FilterLines(0, "-j level=info", opts)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, _, _, _ = items.FilterLines(0, "-j level=info", opts)
			}
		})
	}
}
