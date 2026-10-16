package kit

import (
	"errors"
	"testing"
)

func TestRootsDefaultsAndSplitsOnColon(t *testing.T) {
	t.Setenv("WT_ROOTS", "/a:/b with space:/c")
	got := Roots()
	if len(got) != 3 || got[1] != "/b with space" {
		t.Errorf("got %q", got)
	}
	t.Setenv("WT_ROOTS", "")
	if len(Roots()) == 0 {
		t.Error("want a default when WT_ROOTS is unset")
	}
}

func TestFindNoMatch(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	got, err := Find(nil, "definitely-nothing")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want no matches, got %+v", got)
	}
}

// A bare wt cd, wt cd . or wt cd / from outside every repository asks for a
// place in the repository you are in, and there is none: the error says
// that, not that no worktree matches "/".
func TestFindDotAndRootOutsideARepoSayThereIsNoRepo(t *testing.T) {
	t.Setenv("WT_ROOTS", t.TempDir())
	for _, pattern := range []string{".", "/", ""} {
		got, err := Find(nil, pattern)
		if !errors.Is(err, ErrNotInRepo) || err.Error() != "not inside a git repository" {
			t.Errorf("Find(%q): err %v, want ErrNotInRepo", pattern, err)
		}
		if len(got) != 0 {
			t.Errorf("Find(%q): want no match outside a repository, got %+v", pattern, got)
		}
	}
}
