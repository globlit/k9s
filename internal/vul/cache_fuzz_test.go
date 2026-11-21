// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package vul

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func FuzzCacheLoad(f *testing.F) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	c := newScanCache(filepath.Join(f.TempDir(), "vul-scans"), "v1.0.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.now = func() time.Time { return now }

	c.saveResult(tagImg, db1, now, testScan(tagImg))
	c.saveSBOM(tagImg, testSBOM(tagImg), now)
	for _, ext := range []string{resultExt, sbomExt} {
		bb, err := os.ReadFile(c.path(tagImg, ext))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(bb, tagImg)
		f.Add(bb[:len(bb)/2], tagImg)
		f.Add(bb, pinnedImg)
	}
	f.Add([]byte(""), tagImg)
	f.Add([]byte("{}"), tagImg)
	f.Add([]byte(`{"v":1,"image":"`+tagImg+`","k9sVersion":"v1.0.0","dbSchema":"v6.1.0","dbBuilt":"2026-11-20T00:00:00Z","rows":[["a"]]}`), tagImg)
	f.Add([]byte{0x1f, 0x8b, 0x08, 0x00}, tagImg)

	f.Fuzz(func(t *testing.T, bb []byte, img string) {
		rp := c.path(img, resultExt)
		if err := os.WriteFile(rp, bb, cacheFileMod); err != nil {
			t.Fatal(err)
		}
		sc, ok := c.loadResult(img, db1)
		if json.Valid(bb) {
			if ok {
				for _, r := range sc.Table.Rows {
					if len(r) != sevIdx+1 {
						t.Fatalf("got row %q", r)
					}
				}
			}
		} else {
			if ok {
				t.Fatal("hit on corrupt result")
			}
			if _, err := os.Stat(rp); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("corrupt result is not dropped: %v", err)
			}
		}

		sp := c.path(img, sbomExt)
		if err := os.WriteFile(sp, bb, cacheFileMod); err != nil {
			t.Fatal(err)
		}
		got, _, ok := c.loadSBOM(img)
		if !ok {
			return
		}
		zr, err := gzip.NewReader(bytes.NewReader(bb))
		if err != nil {
			t.Fatalf("hit on bad gzip: %v", err)
		}
		want, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("hit on corrupt gzip: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("SBOM differs from the gzip content")
		}
	})
}
