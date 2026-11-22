// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

//go:build linux

// Package procstats samples a test-owned Linux process without a profiler.
package procstats

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Sample struct {
	At         time.Time `json:"at"`
	CPUSeconds float64   `json:"cpu_seconds"`
	RSSBytes   uint64    `json:"rss_bytes"`
	FDs        int       `json:"fds"`
}

func Read(pid int, ticksPerSecond float64) (Sample, error) {
	return readAt("/proc", pid, ticksPerSecond)
}

func readAt(root string, pid int, ticksPerSecond float64) (Sample, error) {
	if pid <= 0 || ticksPerSecond <= 0 {
		return Sample{}, fmt.Errorf("invalid PID or clock frequency")
	}
	dir := filepath.Join(root, strconv.Itoa(pid))
	raw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return Sample{}, err
	}
	cpu, rss, err := parse(string(raw), ticksPerSecond, uint64(os.Getpagesize()))
	if err != nil {
		return Sample{}, err
	}
	fds, err := os.ReadDir(filepath.Join(dir, "fd"))
	if err != nil {
		return Sample{}, err
	}
	return Sample{At: time.Now(), CPUSeconds: cpu, RSSBytes: rss, FDs: len(fds)}, nil
}

func parse(raw string, ticks float64, pageSize uint64) (float64, uint64, error) {
	// Split after the final ')' because comm can contain spaces and parentheses.
	end := strings.LastIndexByte(raw, ')')
	if end < 0 {
		return 0, 0, fmt.Errorf("missing process name in stat")
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) < 22 {
		return 0, 0, fmt.Errorf("truncated process stat")
	}
	values := make([]uint64, 3)
	for i, index := range []int{11, 12, 21} {
		value, err := strconv.ParseUint(fields[index], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid stat field %d: %w", index+3, err)
		}
		values[i] = value
	}
	return (float64(values[0]) + float64(values[1])) / ticks, values[2] * pageSize, nil
}
