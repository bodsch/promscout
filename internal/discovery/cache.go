package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// loadTargets reads a previously persisted target set from path.
//
// Parameters:
//   - path: the cache file to read.
//
// Returns the decoded targets, or an error if the file cannot be read or
// does not contain valid JSON. A missing file is reported as an error so
// the caller can treat it as "no warm-start data available".
func loadTargets(path string) ([]Target, error) {
	// #nosec G304 -- path is a trusted operator-provided CLI/YAML value.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read target cache: %w", err)
	}

	var targets []Target
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, fmt.Errorf("parse target cache: %w", err)
	}

	return targets, nil
}

// saveTargets atomically persists targets to path.
//
// The data is first written to a temporary file in the same directory,
// flushed to disk, and then renamed onto path. This guarantees a reader
// never observes a partially written file, and that a crash mid-write —
// of the process or of the machine — cannot corrupt an existing cache:
// the rename either happened or it did not.
//
// Parameters:
//   - path: the destination cache file.
//   - targets: the target set to persist.
//
// Returns an error if encoding, writing, or the atomic rename fails.
func saveTargets(path string, targets []Target) error {
	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return fmt.Errorf("encode target cache: %w", err)
	}

	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".promscout-cache-*")
	if err != nil {
		return fmt.Errorf("create temp cache file: %w", err)
	}
	tmpName := tmp.Name()

	// Best-effort cleanup: harmless if the rename already consumed it.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp cache file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp cache file: %w", err)
	}
	// Without this the guarantee above only covers a crashing process, not a
	// crashing machine: Write leaves the bytes in the page cache, and the rename
	// is a metadata operation the filesystem may commit first. A power loss in
	// that window replaces a good cache with an empty or truncated one — the old
	// contents are gone and the new ones never arrived.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp cache file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp cache file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp cache file: %w", err)
	}

	return nil
}
