package admin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// Doctor checks what every --all run reads: a root that is not there, a root
// inside another, a single-repo root wt does not manage, and profile entries
// that are gone or sit outside every root.
func TestDoctorChecksRootsAndProfiles(t *testing.T) {
	a := testkit.RealTempDir(t)
	inside := filepath.Join(a, "inner")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	testkit.ManagedRepo(t, a, "one")
	loose := gittest.NewRepo(t, testkit.RealTempDir(t), "loose")
	outside := testkit.ManagedRepo(t, testkit.RealTempDir(t), "elsewhere")
	u := &config.User{
		Roots: []config.NamedRoot{{Name: "a", Path: a}, {Name: "in", Path: inside},
			{Name: "gone", Path: filepath.Join(a, "nope")}, {Name: "loose", Path: loose}},
		Profiles: []config.Profile{{Name: "p", Repos: []string{filepath.Join(a, "one"), outside, filepath.Join(a, "missing")}}},
	}
	t.Setenv("WT_ROOTS", "")
	var buf bytes.Buffer
	if n := DoctorRepositories(u, nil, &buf); n != 5 {
		t.Errorf("problems = %d, want 5:\n%s", n, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✓ root a: ", "(1 repository)",
		"! root in: ", "is inside root a",
		"! root gone: ", "is not there",
		"! root loose: ", "is one repository, and wt does not manage it",
		"✓ profile p: ", "(in a)",
		"is outside every root",
		"missing is not there any more",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// The listing says per repository whether trunk declares wt sync, and names a
// wt configuration that does not parse; wt doctor --all runs doctor in each,
// and wt sync doctor where trunk declares wt sync.
func TestReposShowsSyncAndABrokenConfiguration(t *testing.T) {
	root := testkit.RealTempDir(t)
	synced := testkit.ManagedRepo(t, root, "synced")
	if err := os.WriteFile(filepath.Join(synced, ".wt-sync.yaml"), []byte("conflicts: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, synced, "add", "-A")
	gittest.Git(t, synced, "commit", "-q", "-m", "declare")
	gittest.WithOrigin(t, synced)
	gittest.WithOrigin(t, testkit.ManagedRepo(t, root, "plain"))
	testkit.ManagedRepo(t, root, "unfetched")
	broken := testkit.ManagedRepo(t, root, "broken")
	if err := os.WriteFile(filepath.Join(broken, "bin", "worktree", "worktree.conf"), []byte("NOT_A_KEY=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := &config.User{Roots: []config.NamedRoot{{Name: "r", Path: root}}}
	t.Setenv("WT_ROOTS", "")

	var buf bytes.Buffer
	if err := kit.Repos(u, kit.Selection{}, kit.ReposOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"synced     no worktrees  sync set up", "plain      no worktrees  no sync",
		"unfetched  no worktrees  sync unknown: origin/main never fetched", "! broken", "/broken  wt configuration: "} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	buf.Reset()
	err := DoctorAll(u, kit.Selection{All: true}, &buf)
	if err == nil || !strings.Contains(buf.String(), "broken     1 problem(s)") || !strings.Contains(buf.String(), "plain      ✓") ||
		!strings.Contains(buf.String(), "~ sync ") {
		t.Errorf("want doctor per repository and a failure: %v\n%s", err, buf.String())
	}
}
