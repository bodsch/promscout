package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveAndLoadTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	want := []Target{
		{Address: "10.0.0.1:9100", Path: "/metrics"},
		{Address: "10.0.0.2:9115", Path: "/"},
	}

	if err := saveTargets(path, want); err != nil {
		t.Fatalf("saveTargets() error = %v", err)
	}

	got, err := loadTargets(path)
	if err != nil {
		t.Fatalf("loadTargets() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadTargets() = %v, want %v", got, want)
	}

	// The cache must be written with restrictive permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file permissions = %o, want 600", perm)
	}
}

func TestSaveTargetsEmptySlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	if err := saveTargets(path, []Target{}); err != nil {
		t.Fatalf("saveTargets() error = %v", err)
	}

	got, err := loadTargets(path)
	if err != nil {
		t.Fatalf("loadTargets() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("loadTargets() = %v, want empty", got)
	}
}

func TestSaveTargetsOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	if err := saveTargets(path, []Target{{Address: "a:1", Path: "/"}}); err != nil {
		t.Fatalf("first saveTargets() error = %v", err)
	}

	want := []Target{{Address: "b:2", Path: "/metrics"}}
	if err := saveTargets(path, want); err != nil {
		t.Fatalf("second saveTargets() error = %v", err)
	}

	got, err := loadTargets(path)
	if err != nil {
		t.Fatalf("loadTargets() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadTargets() = %v, want %v", got, want)
	}

	// No stray temp files must remain in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want 1 (only the cache file)", len(entries))
	}
}

func TestLoadTargetsMissingFile(t *testing.T) {
	_, err := loadTargets(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("loadTargets() expected error for missing file, got nil")
	}
}

func TestLoadTargetsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := loadTargets(path); err == nil {
		t.Fatal("loadTargets() expected error for invalid JSON, got nil")
	}
}
