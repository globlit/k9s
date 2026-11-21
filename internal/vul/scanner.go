// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package vul

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/anchore/clio"
	"github.com/anchore/grype/cmd/grype/cli/options"
	"github.com/anchore/grype/grype"
	"github.com/anchore/grype/grype/match"
	"github.com/anchore/grype/grype/matcher"
	"github.com/anchore/grype/grype/matcher/dotnet"
	"github.com/anchore/grype/grype/matcher/golang"
	"github.com/anchore/grype/grype/matcher/java"
	"github.com/anchore/grype/grype/matcher/javascript"
	"github.com/anchore/grype/grype/matcher/python"
	"github.com/anchore/grype/grype/matcher/ruby"
	"github.com/anchore/grype/grype/matcher/stock"
	"github.com/anchore/grype/grype/pkg"
	"github.com/anchore/grype/grype/vex"
	"github.com/anchore/grype/grype/vulnerability"
	"github.com/anchore/syft/syft"
	"github.com/anchore/syft/syft/cataloging"
	"github.com/anchore/syft/syft/sbom"
	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/slogs"
)

var ImgScanner *imageScanner

const (
	scanConcurrency = 2
)

type imageScanner struct {
	provider    vulnerability.Provider
	status      *vulnerability.ProviderStatus
	opts        *options.Grype
	scans       Scans
	mx          sync.RWMutex
	initialized bool
	config      config.ImageScans
	log         *slog.Logger
	cache       *scanCache

	// Seams for tests: grype's image and SBOM providers, and the matcher.
	provide     func(string, pkg.ProviderConfig) ([]pkg.Package, pkg.Context, *sbom.SBOM, error)
	provideSBOM func(io.ReadSeeker, pkg.ProviderConfig) ([]pkg.Package, pkg.Context, *sbom.SBOM, error)
	match       func([]pkg.Package, pkg.Context, *Scan) error
}

// NewImageScanner returns a new instance.
func NewImageScanner(cfg config.ImageScans, l *slog.Logger) *imageScanner {
	s := imageScanner{
		scans:       make(Scans),
		config:      cfg,
		log:         l.With(slogs.Subsys, "vul"),
		provide:     pkg.Provide,
		provideSBOM: pkg.ProvideFromReader,
	}
	s.match = s.matchPackages

	return &s
}

func (s *imageScanner) ShouldExcludes(ns string, lbls map[string]string) bool {
	return s.config.ShouldExclude(ns, lbls)
}

// GetScan fetch scan for a given image. Returns ok=false when not found.
func (s *imageScanner) GetScan(img string) (*Scan, bool) {
	s.mx.RLock()
	defer s.mx.RUnlock()

	scan, ok := s.scans[img]

	return scan, ok
}

func (s *imageScanner) setScan(img string, sc *Scan) {
	s.mx.Lock()
	defer s.mx.Unlock()

	s.scans[img] = sc
}

// Init initializes image vulnerability database.
func (s *imageScanner) Init(name, version string) {
	defer func(t time.Time) {
		slog.Debug("VulDb initialization complete",
			slogs.Elapsed, time.Since(t),
		)
	}(time.Now())

	opts := options.DefaultGrype(clio.Identification{Name: name, Version: version})
	opts.GenerateMissingCPEs = true

	provider, status, err := grype.LoadVulnerabilityDB(
		opts.ToClientConfig(),
		opts.ToCuratorConfig(),
		opts.DB.AutoUpdate,
	)
	if err != nil {
		s.log.Error("VulDb load failed", slogs.Error, err)
		return
	}
	s.mx.Lock()
	s.opts, s.provider, s.status = opts, provider, status
	s.mx.Unlock()

	if e := validateDBLoad(err, status); e != nil {
		s.log.Error("VulDb validate failed", slogs.Error, e)
		return
	}

	cache := newScanCache(config.AppVulCacheDir, version, s.log)
	go cache.evict()

	s.mx.Lock()
	s.cache, s.initialized = cache, true
	s.mx.Unlock()
	slog.Debug("VulDB initialized")
}

// Stop closes scan database.
func (s *imageScanner) Stop() {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.provider != nil {
		_ = s.provider.Close()
		s.provider = nil
	}
}

func (s *imageScanner) Score(ii ...string) string {
	var sc scorer
	for _, i := range ii {
		if scan, ok := s.GetScan(i); ok {
			sc = sc.Add(newScorer(scan.Tally))
		}
	}

	return sc.String()
}

func (s *imageScanner) IsInitialized() bool {
	s.mx.RLock()
	defer s.mx.RUnlock()

	return s.initialized
}

// scanSem bounds concurrent scans: each one pulls and unpacks an image.
var scanSem = make(chan struct{}, scanConcurrency)

func (s *imageScanner) Enqueue(_ context.Context, images ...string) {
	for _, img := range images {
		// Reserve atomically: tables call this for every row on every
		// refresh, so 50 replicas of an image must not start 50 scans.
		s.mx.Lock()
		if _, ok := s.scans[img]; ok {
			s.mx.Unlock()
			continue
		}
		s.scans[img] = newScan(img)
		s.mx.Unlock()

		go s.scanWorker(img)
	}
}

func (s *imageScanner) scanWorker(img string) {
	defer s.log.Debug("ScanWorker bailing out!")
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("Scan panicked", slogs.Image, img, slogs.Error, r)
		}
	}()

	// Results matched against the loaded DB need neither network nor CPU.
	if sc, ok := s.cache.loadResult(img, s.dbStamp()); ok {
		s.log.Debug("[Vulscan] cached result", slogs.Image, img)
		s.setScan(img, sc)
		return
	}

	scanSem <- struct{}{}
	defer func() { <-scanSem }()

	s.log.Debug("ScanWorker processing image", slogs.Image, img)
	// Fill a private scan and publish it when complete: renderers read
	// published scans concurrently.
	sc := newScan(img)
	if err := s.scan(img, sc); err != nil {
		s.log.Warn("Scan failed for image",
			slogs.Image, img,
			slogs.Error, err,
		)
	}
	s.setScan(img, sc)
}

func (s *imageScanner) dbStamp() dbStamp {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.status == nil {
		return dbStamp{}
	}

	return dbStamp{Built: s.status.Built, Schema: s.status.SchemaVersion}
}

// scan catalogs the image, from the cached SBOM when there is a fresh one,
// and matches its packages. Only complete scans are cached.
func (s *imageScanner) scan(img string, sc *Scan) error {
	defer func(t time.Time) {
		s.log.Debug("[Vulscan] perf",
			slogs.Image, img,
			slogs.Elapsed, time.Since(t),
		)
	}(time.Now())

	var (
		cfg      = getProviderConfig(s.opts)
		packages []pkg.Package
		pkgCtx   pkg.Context
		sbomAt   time.Time
		cached   bool
	)
	if bb, at, ok := s.cache.loadSBOM(img); ok {
		var err error
		if packages, pkgCtx, _, err = s.provideSBOM(bytes.NewReader(bb), cfg); err != nil {
			s.cache.dropSBOM(img, err)
		} else {
			s.log.Debug("[Vulscan] cached SBOM", slogs.Image, img)
			sbomAt, cached = at, true
		}
	}
	if !cached {
		var (
			doc *sbom.SBOM
			err error
		)
		if packages, pkgCtx, doc, err = s.provide(img, cfg); err != nil {
			return fmt.Errorf("failed to analyze image packages: %w", err)
		}
		sbomAt = time.Now()
		s.cache.saveSBOM(img, doc, sbomAt)
	}

	if err := s.match(packages, pkgCtx, sc); err != nil {
		return err
	}
	s.cache.saveResult(img, s.dbStamp(), sbomAt, sc)

	return nil
}

func (s *imageScanner) matchPackages(packages []pkg.Package, pkgCtx pkg.Context, sc *Scan) error {
	processor, err := vex.NewProcessor(vex.ProcessorOptions{
		Documents:   s.opts.VexDocuments,
		IgnoreRules: s.opts.Ignore,
	})
	if err != nil {
		return fmt.Errorf("failed to create VEX processor: %w", err)
	}

	v := grype.VulnerabilityMatcher{
		VulnerabilityProvider: s.provider,
		IgnoreRules:           s.opts.Ignore,
		NormalizeByCVE:        s.opts.ByCVE,
		FailSeverity:          s.opts.FailOnSeverity(),
		Matchers:              getMatchers(s.opts),
		VexProcessor:          processor,
	}

	var errs error
	mm, _, err := v.FindMatches(packages, pkgCtx)
	if err != nil {
		errs = errors.Join(errs, err)
	}
	if err := sc.run(mm, s.provider); err != nil {
		errs = errors.Join(errs, err)
	}

	return errs
}

func getProviderConfig(opts *options.Grype) pkg.ProviderConfig {
	// Like grype's CLI: packages without a version can't match anything,
	// drop them before hitting the DB.
	sbomCfg := syft.DefaultCreateSBOMConfig()
	sbomCfg.Compliance.MissingVersion = cataloging.ComplianceActionDrop

	return pkg.ProviderConfig{
		SyftProviderConfig: pkg.SyftProviderConfig{
			SBOMOptions:            sbomCfg,
			RegistryOptions:        opts.Registry.ToOptions(),
			Exclusions:             opts.Exclusions,
			Platform:               opts.Platform,
			Name:                   opts.Name,
			DefaultImagePullSource: opts.DefaultImagePullSource,
		},
		SynthesisConfig: pkg.SynthesisConfig{
			GenerateMissingCPEs: opts.GenerateMissingCPEs,
		},
	}
}

func getMatchers(opts *options.Grype) []match.Matcher {
	return matcher.NewDefaultMatchers(
		matcher.Config{
			Java: java.MatcherConfig{
				ExternalSearchConfig: opts.ExternalSources.ToJavaMatcherConfig(),
				UseCPEs:              opts.Match.Java.UseCPEs,
			},
			Ruby:       ruby.MatcherConfig(opts.Match.Ruby),
			Python:     python.MatcherConfig(opts.Match.Python),
			Dotnet:     dotnet.MatcherConfig(opts.Match.Dotnet),
			Javascript: javascript.MatcherConfig(opts.Match.Javascript),
			Golang: golang.MatcherConfig{
				UseCPEs:                                opts.Match.Golang.UseCPEs,
				AlwaysUseCPEForStdlib:                  opts.Match.Golang.AlwaysUseCPEForStdlib,
				AllowMainModulePseudoVersionComparison: opts.Match.Golang.AllowMainModulePseudoVersionComparison,
			},
			Stock: stock.MatcherConfig(opts.Match.Stock),
		},
	)
}
func validateDBLoad(loadErr error, status *vulnerability.ProviderStatus) error {
	if loadErr != nil {
		return fmt.Errorf("failed to load vulnerability db: %w", loadErr)
	}
	if status == nil {
		return fmt.Errorf("unable to determine the status of the vulnerability db")
	}
	if status.Error != nil {
		return fmt.Errorf("db could not be loaded: %w", status.Error)
	}

	return nil
}
