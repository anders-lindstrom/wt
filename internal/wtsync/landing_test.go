package wtsync

import (
	"os"
	"path/filepath"
	"testing"
)

// A direct commit carries its own scope; a merge commit's subject has none
// ("Merge pull request #N from …"), so its scopes come from the range it
// merged (spec §5).
func TestLandingListCountsDirectAndMergedScopes(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("a.txt", "a2\n")
	gitIn(t, dir, "commit", "-qam", "feat(pins): move pin quality out")

	gitIn(t, dir, "checkout", "-q", "-b", "side")
	write("c.txt", "c\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "fix(auth): count endings by reason")
	write("c.txt", "c2\n")
	gitIn(t, dir, "commit", "-qam", "feat(pins): pin quality again")
	gitIn(t, dir, "checkout", "-q", "main")
	gitIn(t, dir, "merge", "-q", "--no-ff", "-m", "Merge pull request #1 from x/side", "side")

	l, err := LandingList(dir, base, "main")
	if err != nil {
		t.Fatal(err)
	}
	if l.Commits != 2 {
		t.Fatalf("Commits = %d, want 2 first-parent commits", l.Commits)
	}
	if got := l.ScopeLine(); got != "pins ×2, auth ×1" {
		t.Fatalf("ScopeLine = %q, want %q", got, "pins ×2, auth ×1")
	}
}

func TestLandingListWithNoScopes(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	base := gitIn(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "commit", "-qam", "tidy up")

	l, err := LandingList(dir, base, "main")
	if err != nil {
		t.Fatal(err)
	}
	if l.Commits != 1 || l.ScopeLine() != "" {
		t.Fatalf("Landing = %+v, ScopeLine = %q", l, l.ScopeLine())
	}
}
