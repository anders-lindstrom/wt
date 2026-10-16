package testkit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// quarantineDir is a new folder name next to the repository, on its volume.
func quarantineDir(t *testing.T, ctx *kit.Context) string {
	t.Helper()
	return filepath.Join(ctx.Repo.Parent, "trash", "q-"+strings.ReplaceAll(t.Name(), "/", "-"))
}

// TrashFor is the quarantine folder for the repository, made.
func TrashFor(t *testing.T, ctx *kit.Context) string {
	t.Helper()
	dir := quarantineDir(t, ctx)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// LoadRecord reads the quarantine record in dir.
func LoadRecord(t *testing.T, dir string) *quarantine.Record {
	t.Helper()
	r, err := quarantine.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ErrCrash ends a run at a journal write, as a crash there would.
var ErrCrash = errors.New("crash")

// CrashAt makes the run stop right after the journal write named point.
func CrashAt(t *testing.T, point string) {
	t.Helper()
	t.Cleanup(func() { quarantine.AfterSave = nil })
	quarantine.AfterSave = func(_ *quarantine.Record, at string) error {
		if at == point {
			return ErrCrash
		}
		return nil
	}
}
