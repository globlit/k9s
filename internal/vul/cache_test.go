// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package vul

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anchore/clio"
	"github.com/anchore/grype/cmd/grype/cli/options"
	"github.com/anchore/grype/grype/pkg"
	"github.com/anchore/grype/grype/vulnerability"
	"github.com/anchore/syft/syft/cataloging"
	syftPkg "github.com/anchore/syft/syft/pkg"
	"github.com/anchore/syft/syft/sbom"
	"github.com/anchore/syft/syft/source"
	"github.com/derailed/k9s/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tagImg    = "ghcr.io/acme/web:1.2"
	pinnedImg = "ghcr.io/acme/web@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

var (
	db1 = dbStamp{Built: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), Schema: "v6.1.0"}
	db2 = dbStamp{Built: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC), Schema: "v6.1.0"}
)

func testCache(t *testing.T) (*scanCache, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	c := newScanCache(filepath.Join(t.TempDir(), "vul-scans"), "v1.0.0", slog.Default())
	c.now = func() time.Time { return now }

	return c, &now
}

func testScan(img string) *Scan {
	sc := newScan(img)
	sc.Table.addRow(newRow("busybox", "1.36.1", "1.36.2", "apk", "CVE-2026-0001", "High"))
	sc.Table.addRow(newRow("zlib", "1.3", "", "apk", "CVE-2026-0002", "Low"))
	sc.Tally = newTally(sc.Table)

	return sc
}

func testSBOM(img string) *sbom.SBOM {
	p := syftPkg.Package{Name: "busybox", Version: "1.36.1", Type: syftPkg.ApkPkg}
	p.SetID()

	return &sbom.SBOM{
		Artifacts: sbom.Artifacts{Packages: syftPkg.NewCollection(p)},
		Source: source.Description{
			Name:     img,
			Metadata: source.ImageMetadata{UserInput: img},
		},
	}
}

func TestCacheResultRoundTrip(t *testing.T) {
	c, now := testCache(t)
	at := now.Add(-time.Hour)
	c.saveResult(tagImg, db1, at, testScan(tagImg))

	sc, ok := c.loadResult(tagImg, db1)
	require.True(t, ok)
	assert.Equal(t, testScan(tagImg).Table.Rows, sc.Table.Rows)
	assert.Equal(t, testScan(tagImg).Tally, sc.Tally)

	_, ok = c.loadResult(tagImg, db2)
	assert.False(t, ok, "a DB update invalidates results")
	_, ok = c.loadResult("ghcr.io/acme/other:1", db1)
	assert.False(t, ok)

	c2 := newScanCache(c.dir, "v1.0.1", slog.Default())
	_, ok = c2.loadResult(tagImg, db1)
	assert.False(t, ok, "another k9s version rescans")

	info, err := os.Stat(c.dir)
	require.NoError(t, err)
	assert.Equal(t, cacheDirMod, info.Mode().Perm())
	info, err = os.Stat(c.path(tagImg, resultExt))
	require.NoError(t, err)
	assert.Equal(t, cacheFileMod, info.Mode().Perm())
}

func TestCacheTagTTL(t *testing.T) {
	c, now := testCache(t)
	at := now.Add(-2 * tagTTL)
	c.saveResult(tagImg, db1, at, testScan(tagImg))
	c.saveResult(pinnedImg, db1, at, testScan(pinnedImg))
	c.saveSBOM(tagImg, testSBOM(tagImg), at)
	c.saveSBOM(pinnedImg, testSBOM(pinnedImg), at)

	_, ok := c.loadResult(tagImg, db1)
	assert.False(t, ok, "tags are mutable")
	_, _, ok = c.loadSBOM(tagImg)
	assert.False(t, ok, "tags are mutable")

	_, ok = c.loadResult(pinnedImg, db1)
	assert.True(t, ok, "digests are immutable")
	_, sbomAt, ok := c.loadSBOM(pinnedImg)
	assert.True(t, ok, "digests are immutable")
	assert.True(t, at.Equal(sbomAt), "catalog time survives mtime refreshes")
}

func TestCacheSBOMDecodesWithGrype(t *testing.T) {
	c, now := testCache(t)
	c.saveSBOM(tagImg, testSBOM(tagImg), *now)

	bb, _, ok := c.loadSBOM(tagImg)
	require.True(t, ok)
	pp, _, _, err := pkg.ProvideFromReader(bytes.NewReader(bb), getProviderConfig(testOpts()))
	require.NoError(t, err)
	require.Len(t, pp, 1)
	assert.Equal(t, "busybox", pp[0].Name)
	assert.Equal(t, "1.36.1", pp[0].Version)
}

func TestCacheCorruptEntriesAreDropped(t *testing.T) {
	c, _ := testCache(t)
	require.NoError(t, os.MkdirAll(c.dir, cacheDirMod))
	rp, sp := c.path(tagImg, resultExt), c.path(tagImg, sbomExt)
	require.NoError(t, os.WriteFile(rp, []byte("{nope"), cacheFileMod))
	require.NoError(t, os.WriteFile(sp, []byte("not gzip"), cacheFileMod))

	_, ok := c.loadResult(tagImg, db1)
	assert.False(t, ok)
	_, _, ok = c.loadSBOM(tagImg)
	assert.False(t, ok)
	assert.NoFileExists(t, rp)
	assert.NoFileExists(t, sp)
}

func TestCacheNilIsNoop(t *testing.T) {
	var c *scanCache
	c.saveResult(tagImg, db1, time.Now(), testScan(tagImg))
	c.saveSBOM(tagImg, testSBOM(tagImg), time.Now())
	c.evict()
	_, ok := c.loadResult(tagImg, db1)
	assert.False(t, ok)
	_, _, ok = c.loadSBOM(tagImg)
	assert.False(t, ok)
	assert.Nil(t, newScanCache("", "v1", slog.Default()))
}

func TestCacheEvict(t *testing.T) {
	c, now := testCache(t)
	require.NoError(t, os.MkdirAll(c.dir, cacheDirMod))
	file := func(n string, size int, age time.Duration) string {
		p := filepath.Join(c.dir, n)
		require.NoError(t, os.WriteFile(p, make([]byte, size), cacheFileMod))
		mod := now.Add(-age)
		require.NoError(t, os.Chtimes(p, mod, mod))
		return p
	}
	old := file("a"+resultExt, 10, cacheMaxAge+time.Hour)
	staleTmp := file(".b"+resultExt+".123.tmp", 10, 2*time.Hour)
	liveTmp := file(".c"+resultExt+".456.tmp", 10, time.Minute)
	foreign := file("README", 10, cacheMaxAge+time.Hour)
	lru := file("d"+sbomExt, 600, 3*time.Hour)
	mru := file("e"+sbomExt, 600, time.Hour)

	c.maxBytes = 1000
	c.evict()

	assert.NoFileExists(t, old)
	assert.NoFileExists(t, staleTmp)
	assert.FileExists(t, liveTmp, "may belong to a running writer")
	assert.FileExists(t, foreign)
	assert.NoFileExists(t, lru, "oldest goes first when over the size cap")
	assert.FileExists(t, mru)
}

func TestCacheConcurrentWriters(t *testing.T) {
	c, now := testCache(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.saveResult(tagImg, db1, *now, testScan(tagImg))
			c.saveSBOM(tagImg, testSBOM(tagImg), *now)
		}()
	}
	wg.Wait()

	_, ok := c.loadResult(tagImg, db1)
	assert.True(t, ok)
	_, _, ok = c.loadSBOM(tagImg)
	assert.True(t, ok)
	ee, err := os.ReadDir(c.dir)
	require.NoError(t, err)
	assert.Len(t, ee, 2, "no temp files left behind")
}

func TestProviderConfigDropsVersionlessPackages(t *testing.T) {
	cfg := getProviderConfig(testOpts())
	assert.Equal(t, cataloging.ComplianceActionDrop, cfg.SBOMOptions.Compliance.MissingVersion)
}

func testOpts() *options.Grype {
	return options.DefaultGrype(clio.Identification{Name: "k9s", Version: "test"})
}

// fakeScanner wires a scanner to a cache with stubbed providers and matcher.
type fakeScanner struct {
	*imageScanner
	provided, fromSBOM, matched int
}

func newFakeScanner(t *testing.T, dir string, db dbStamp) *fakeScanner {
	t.Helper()
	s := NewImageScanner(config.ImageScans{Enable: true}, slog.Default())
	s.opts = testOpts()
	s.status = &vulnerability.ProviderStatus{Built: db.Built, SchemaVersion: db.Schema}
	s.cache = newScanCache(dir, "v1.0.0", slog.Default())
	f := fakeScanner{imageScanner: s}
	s.provide = func(img string, _ pkg.ProviderConfig) ([]pkg.Package, pkg.Context, *sbom.SBOM, error) {
		f.provided++
		return nil, pkg.Context{}, testSBOM(img), nil
	}
	real := s.provideSBOM
	s.provideSBOM = func(r io.ReadSeeker, cfg pkg.ProviderConfig) ([]pkg.Package, pkg.Context, *sbom.SBOM, error) {
		f.fromSBOM++
		return real(r, cfg)
	}
	s.match = func(_ []pkg.Package, _ pkg.Context, sc *Scan) error {
		f.matched++
		*sc = *testScan(sc.ID)
		return nil
	}

	return &f
}

func TestScannerUsesCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vul-scans")

	// Cold: pull/catalog, match, persist both levels.
	s := newFakeScanner(t, dir, db1)
	s.scanWorker(pinnedImg)
	assert.Equal(t, [3]int{1, 0, 1}, [3]int{s.provided, s.fromSBOM, s.matched})
	sc, ok := s.GetScan(pinnedImg)
	require.True(t, ok)
	want := sc.Tally

	// Restart, same DB: result hit, no work at all.
	s = newFakeScanner(t, dir, db1)
	s.scanWorker(pinnedImg)
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{s.provided, s.fromSBOM, s.matched})
	sc, ok = s.GetScan(pinnedImg)
	require.True(t, ok)
	assert.Equal(t, want, sc.Tally)

	// Restart after a DB update: re-match from the cached SBOM, no pull.
	s = newFakeScanner(t, dir, db2)
	s.scanWorker(pinnedImg)
	assert.Equal(t, [3]int{0, 1, 1}, [3]int{s.provided, s.fromSBOM, s.matched})

	// And that new result is now cached for db2.
	s = newFakeScanner(t, dir, db2)
	s.scanWorker(pinnedImg)
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{s.provided, s.fromSBOM, s.matched})
}

func TestScannerDoesNotCacheFailures(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vul-scans")
	s := newFakeScanner(t, dir, db1)
	s.match = func([]pkg.Package, pkg.Context, *Scan) error { return assert.AnError }
	s.scanWorker(tagImg)
	_, ok := s.GetScan(tagImg)
	assert.True(t, ok, "failed scans still publish, as before")

	s2 := newFakeScanner(t, dir, db1)
	s2.scanWorker(tagImg)
	assert.Equal(t, 1, s2.matched, "a failed match is retried next session")
	assert.Equal(t, 1, s2.fromSBOM, "but its SBOM is reused")
}
