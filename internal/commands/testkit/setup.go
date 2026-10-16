package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MustMkdir makes p and its parents.
func MustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

// MustWrite writes body to p, making its folder.
func MustWrite(t *testing.T, p, body string) {
	t.Helper()
	MustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// MustRead is the trimmed content of p.
func MustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
