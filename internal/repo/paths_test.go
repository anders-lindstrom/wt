package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInside(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		what string
		dir  string
		p    string
		want bool
	}{
		{"the directory itself", dir, dir, true},
		{"a path under it", dir, sub, true},
		{"a sibling sharing its prefix", dir, dir + "-other", false},
		{"a path that climbs back out", sub, filepath.Join(sub, "..", "..", "etc"), false},
		{"its own parent", sub, dir, false},
	}
	for _, c := range cases {
		if got := Inside(c.dir, c.p, false); got != c.want {
			t.Errorf("%s: Inside(%q, %q) = %v, want %v", c.what, c.dir, c.p, got, c.want)
		}
	}
}

// A worktree path can carry a symlink that the other path has already
// resolved, which is the case resolve is there for; without it the comparison
// stays a string one.
func TestInsideResolvesSymlinksOnlyWhenAsked(t *testing.T) {
	target := t.TempDir()
	sub := filepath.Join(target, "inner")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if Inside(link, sub, false) {
		t.Error("without resolve, the two spellings must not match")
	}
	if !Inside(link, sub, true) {
		t.Error("with resolve, they name the same tree")
	}
	if !SamePath(link, target) {
		t.Error("SamePath resolves both sides")
	}
}
