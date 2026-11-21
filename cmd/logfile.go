// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package cmd

import (
	"os"
	"sync"

	"github.com/derailed/k9s/internal/config/data"
)

// maxLogSize caps k9s.log. Rotation keeps one backup, k9s.log.1.
const maxLogSize = 50 * 1024 * 1024

// rotatingFile appends to a log file and rotates it past max bytes.
type rotatingFile struct {
	mx   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func openRotatingFile(path string, maxSize int64) (*rotatingFile, error) {
	r := rotatingFile{path: path, max: maxSize}
	if err := r.open(); err != nil {
		return nil, err
	}
	if r.size >= r.max {
		r.rotate()
	}

	return &r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, data.DefaultFileMod)
	if err != nil {
		return err
	}
	r.f, r.size = f, 0
	if fi, err := f.Stat(); err == nil {
		r.size = fi.Size()
	}

	return nil
}

// rotate moves the log aside and opens a new one. On failure, keep writing
// to the current file.
func (r *rotatingFile) rotate() {
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return
	}
	old := r.f
	if err := r.open(); err != nil {
		r.f = old
		return
	}
	_ = old.Close()
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mx.Lock()
	defer r.mx.Unlock()

	if r.size+int64(len(p)) > r.max {
		r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)

	return n, err
}

func (r *rotatingFile) Close() error {
	r.mx.Lock()
	defer r.mx.Unlock()

	return r.f.Close()
}
