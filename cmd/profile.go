// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"log/slog"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"runtime/debug"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/derailed/k9s/internal/slogs"
	"github.com/derailed/k9s/internal/ui"
)

const (
	// envCPUProfile names the session CPU profile output path.
	envCPUProfile = "K9S_CPUPROFILE"
	// envPprofAddr names the loopback address serving net/http/pprof.
	envPprofAddr = "K9S_PPROF_ADDR"

	// defaultMemoryLimit sets the soft memory limit when GOMEMLIMIT is unset.
	defaultMemoryLimit = 2 << 30
)

// startProfiling enables opt-in profiling and returns a flush func.
// BailOut calls os.Exit, so also register the flush as an exit hook.
func startProfiling() func() {
	var stops []func()

	if path := os.Getenv(envCPUProfile); path != "" {
		if f, err := os.Create(path); err != nil {
			slog.Warn("CPU profile disabled", slogs.Path, path, slogs.Error, err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			_ = f.Close()
			slog.Warn("CPU profile disabled", slogs.Path, path, slogs.Error, err)
		} else {
			slog.Info("CPU profiling enabled", slogs.Path, path)
			stops = append(stops, func() {
				pprof.StopCPUProfile()
				_ = f.Close()
			})
		}
	}

	if addr := os.Getenv(envPprofAddr); addr != "" {
		servePprof(addr)
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			for _, s := range stops {
				s()
			}
		})
	}
	ui.OnExit(stop)

	return stop
}

// servePprof serves pprof on loopback only: profiles expose memory contents.
func servePprof(addr string) {
	if !isLoopbackAddr(addr) {
		slog.Warn("Refusing pprof on a non-loopback address", slogs.Address, addr)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", httppprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		slog.Info("pprof listening", slogs.Address, addr)
		if err := srv.ListenAndServe(); err != nil {
			slog.Warn("pprof server stopped", slogs.Error, err)
		}
	}()
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// applyMemoryLimit sets the default soft memory limit unless GOMEMLIMIT is set.
func applyMemoryLimit() {
	if _, ok := os.LookupEnv("GOMEMLIMIT"); ok {
		slog.Debug("Memory limit from GOMEMLIMIT", "limit", debug.SetMemoryLimit(-1))
		return
	}
	debug.SetMemoryLimit(defaultMemoryLimit)
	slog.Debug("Default soft memory limit set", "limit", defaultMemoryLimit)
}
