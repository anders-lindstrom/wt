package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// TreeOf is every path under dir, symlinks not followed; nil when there is
// no dir.
func TreeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// PinsOf lists the quarantine pins of the repository.
func PinsOf(t *testing.T, ctx *kit.Context) string {
	t.Helper()
	return strings.TrimSpace(GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "--format=%(objectname) %(refname)",
		quarantine.PinPrefix))
}

// AssertPurged checks the folder and every pin are gone.
func AssertPurged(t *testing.T, ctx *kit.Context, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("the folder must be gone: %v %v", err, TreeOf(t, dir))
	}
	if pins := PinsOf(t, ctx); pins != "" {
		t.Errorf("the pins must be gone:\n%s", pins)
	}
}
