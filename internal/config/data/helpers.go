// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package data

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

const envFGNodeShell = "K9S_FEATURE_GATE_NODE_SHELL"

var invalidPathCharsRX = regexp.MustCompile(`[:/]+`)

// SanitizeContextSubpath ensure cluster/context produces a valid path.
func SanitizeContextSubpath(cluster, context string) string {
	return filepath.Join(SanitizeFileName(cluster), SanitizeFileName(context))
}

// SanitizeFileName ensure file spec is valid.
func SanitizeFileName(name string) string {
	return invalidPathCharsRX.ReplaceAllString(name, "-")
}

func defaultFGNodeShell() bool {
	if a := os.Getenv(envFGNodeShell); a != "" {
		return a == "true"
	}

	return false
}

// EnsureDirPath ensures a directory exist from the given path.
func EnsureDirPath(path string, mod os.FileMode) error {
	return EnsureFullPath(filepath.Dir(path), mod)
}

// EnsureFullPath ensures a directory exist from the given path.
func EnsureFullPath(path string, mod os.FileMode) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if e := os.MkdirAll(path, mod); e != nil {
			return e
		}
	}

	return nil
}

// WriteYAML writes a yaml file to bytes.
func WriteYAML(content any) ([]byte, error) {
	buff := bytes.NewBuffer(nil)
	ec := yaml.NewEncoder(buff)
	ec.SetIndent(2)

	if err := ec.Encode(content); err != nil {
		return nil, err
	}

	return buff.Bytes(), nil
}

// SaveYAML writes a yaml file to disk.
func SaveYAML(path string, content any) error {
	bb, err := WriteYAML(content)
	if err != nil {
		return err
	}

	return WriteFileAtomic(path, bb, DefaultFileMod)
}

// WriteFileAtomic writes bb to a synced temp file, then renames it over path.
// Resolve symlinks first to keep links intact.
func WriteFileAtomic(path string, bb []byte, mod os.FileMode) (err error) {
	if p, e := filepath.EvalSymlinks(path); e == nil {
		path = p
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if _, err = f.Write(bb); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp, mod); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}
