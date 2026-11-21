// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package vul

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anchore/syft/syft/format/syftjson"
	"github.com/anchore/syft/syft/sbom"
	"github.com/derailed/k9s/internal/config/data"
	"github.com/derailed/k9s/internal/slogs"
)

const (
	// cacheFormat is bumped whenever the on-disk layout changes.
	cacheFormat = 1

	// tagTTL bounds how long the SBOM of a tag ref is trusted: tags are
	// mutable, digests are not.
	tagTTL = 24 * time.Hour

	// cacheMaxAge evicts entries nobody used for that long.
	cacheMaxAge = 30 * 24 * time.Hour

	// cacheMaxBytes caps the cache size, oldest entries go first.
	cacheMaxBytes int64 = 1 << 30

	cacheDirMod  os.FileMode = 0o700
	cacheFileMod os.FileMode = 0o600

	sbomExt   = ".sbom.json.gz"
	resultExt = ".result.json"
)

// dbStamp identifies a vulnerability DB build. Results are only reused
// against the very DB they were matched with.
type dbStamp struct {
	Built  time.Time
	Schema string
}

// cachedResult is the on-disk form of a scan.
type cachedResult struct {
	Format     int       `json:"v"`
	Image      string    `json:"image"`
	DBBuilt    time.Time `json:"dbBuilt"`
	DBSchema   string    `json:"dbSchema"`
	K9sVersion string    `json:"k9sVersion"`
	// SBOMAt is when the image was cataloged, results of a tag ref expire
	// with its SBOM.
	SBOMAt time.Time `json:"sbomAt"`
	Rows   []Row     `json:"rows"`
}

// scanCache persists SBOMs and scan results across k9s sessions, so a
// restart neither re-pulls nor re-scans unchanged images. Two levels:
//   - the SBOM (expensive: image pull + cataloging), reusable against any DB;
//   - the result, only valid for the DB build and k9s version it came from.
//
// A nil cache is valid and caches nothing.
type scanCache struct {
	dir     string
	version string
	log     *slog.Logger
	now     func() time.Time

	maxBytes int64
}

func newScanCache(dir, version string, l *slog.Logger) *scanCache {
	if dir == "" {
		return nil
	}

	return &scanCache{dir: dir, version: version, log: l, now: time.Now, maxBytes: cacheMaxBytes}
}

func cacheKey(img string) string {
	h := sha256.Sum256([]byte(img))
	return hex.EncodeToString(h[:])
}

func (c *scanCache) path(img, ext string) string {
	return filepath.Join(c.dir, cacheKey(img)+ext)
}

// isPinned reports whether the ref names an immutable digest.
func isPinned(img string) bool {
	return strings.Contains(img, "@")
}

func (c *scanCache) fresh(img string, at time.Time) bool {
	return isPinned(img) || c.now().Sub(at) < tagTTL
}

func (c *scanCache) touch(p string) {
	n := c.now()
	_ = os.Chtimes(p, n, n)
}

func (c *scanCache) drop(p string, err error) {
	c.log.Debug("Dropping vulnerability cache entry", slogs.Path, p, slogs.Error, err)
	_ = os.Remove(p)
}

func (c *scanCache) write(p string, bb []byte) error {
	if err := os.MkdirAll(c.dir, cacheDirMod); err != nil {
		return err
	}

	return data.WriteFileAtomic(p, bb, cacheFileMod)
}

// loadResult returns a cached scan valid for the given DB.
func (c *scanCache) loadResult(img string, db dbStamp) (*Scan, bool) {
	if c == nil {
		return nil, false
	}
	p := c.path(img, resultExt)
	bb, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var r cachedResult
	if err := json.Unmarshal(bb, &r); err != nil {
		c.drop(p, err)
		return nil, false
	}
	if r.Format != cacheFormat || r.Image != img || r.K9sVersion != c.version ||
		!r.DBBuilt.Equal(db.Built) || r.DBSchema != db.Schema || !c.fresh(img, r.SBOMAt) {
		return nil, false
	}
	for _, row := range r.Rows {
		if len(row) != sevIdx+1 {
			c.drop(p, errors.New("invalid row"))
			return nil, false
		}
	}
	c.touch(p)
	sc := newScan(img)
	sc.Table.Rows = r.Rows
	sc.Tally = newTally(sc.Table)

	return sc, true
}

func (c *scanCache) saveResult(img string, db dbStamp, sbomAt time.Time, sc *Scan) {
	if c == nil {
		return
	}
	bb, err := json.Marshal(cachedResult{
		Format:     cacheFormat,
		Image:      img,
		DBBuilt:    db.Built,
		DBSchema:   db.Schema,
		K9sVersion: c.version,
		SBOMAt:     sbomAt,
		Rows:       sc.Table.Rows,
	})
	if err == nil {
		err = c.write(c.path(img, resultExt), bb)
	}
	if err != nil {
		c.log.Warn("Unable to cache scan result", slogs.Image, img, slogs.Error, err)
	}
}

// loadSBOM returns a still fresh syft-json SBOM and when it was cataloged.
func (c *scanCache) loadSBOM(img string) ([]byte, time.Time, bool) {
	if c == nil {
		return nil, time.Time{}, false
	}
	p := c.path(img, sbomExt)
	f, err := os.Open(p)
	if err != nil {
		return nil, time.Time{}, false
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		c.drop(p, err)
		return nil, time.Time{}, false
	}
	// The gzip header carries the image ref and the catalog time: mtimes
	// are refreshed on every hit.
	at := zr.ModTime
	if zr.Name != img {
		return nil, time.Time{}, false
	}
	if !c.fresh(img, at) {
		return nil, time.Time{}, false
	}
	bb, err := io.ReadAll(zr)
	if err != nil {
		c.drop(p, err)
		return nil, time.Time{}, false
	}
	c.touch(p)

	return bb, at, true
}

func (c *scanCache) dropSBOM(img string, err error) {
	if c != nil {
		c.drop(c.path(img, sbomExt), err)
	}
}

func (c *scanCache) saveSBOM(img string, s *sbom.SBOM, at time.Time) {
	if c == nil || s == nil {
		return
	}
	var buff bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buff, gzip.BestSpeed)
	if err == nil {
		zw.Name, zw.ModTime = img, at
		err = syftjson.NewFormatEncoder().Encode(zw, *s)
		if e := zw.Close(); err == nil {
			err = e
		}
	}
	if err == nil {
		err = c.write(c.path(img, sbomExt), buff.Bytes())
	}
	if err != nil {
		c.log.Warn("Unable to cache image SBOM", slogs.Image, img, slogs.Error, err)
	}
}

// evict removes stale temp files, entries unused for cacheMaxAge, then the
// least recently used ones until the cache fits in maxBytes.
func (c *scanCache) evict() {
	if c == nil {
		return
	}
	ee, err := os.ReadDir(c.dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.log.Warn("Unable to read vulnerability cache", slogs.Dir, c.dir, slogs.Error, err)
		}
		return
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var (
		kept  []entry
		total int64
		now   = c.now()
	)
	for _, e := range ee {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p, n := filepath.Join(c.dir, e.Name()), e.Name()
		isTmp := strings.HasPrefix(n, ".") && strings.HasSuffix(n, ".tmp")
		switch {
		case isTmp && now.Sub(info.ModTime()) > time.Hour:
			_ = os.Remove(p)
		case isTmp:
		case !strings.HasSuffix(n, sbomExt) && !strings.HasSuffix(n, resultExt):
		case now.Sub(info.ModTime()) > cacheMaxAge:
			_ = os.Remove(p)
		default:
			kept, total = append(kept, entry{path: p, size: info.Size(), mod: info.ModTime()}), total+info.Size()
		}
	}
	if total <= c.maxBytes {
		return
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].mod.Before(kept[j].mod) })
	for _, e := range kept {
		if total <= c.maxBytes {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
		}
	}
}
