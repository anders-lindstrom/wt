package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// UserConfigIn points this test's user config at a directory of its own.
func UserConfigIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "wt", "config.toml")
}

// WriteUserConfig puts body at the user config path for this test.
func WriteUserConfig(t *testing.T, body string) string {
	t.Helper()
	path := UserConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
