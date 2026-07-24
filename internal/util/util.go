// Package util holds small helpers shared across packages that would
// otherwise create import cycles (e.g. bridge <-> games/clair).
package util

import (
	"os"
	"path/filepath"
)

// BoolValue coerces a loosely-typed value (as decoded from JSON or CLI
// input) into a bool, returning fallback for anything it can't interpret.
func BoolValue(value any, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "1"
	default:
		return fallback
	}
}

// AtomicWrite writes data to path via a temp file in the same directory,
// fsyncing before rename so a crash never leaves a partially written file.
func AtomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// CopyFile reads source and atomically writes it to dest.
func CopyFile(source, dest string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return AtomicWrite(dest, data)
}
