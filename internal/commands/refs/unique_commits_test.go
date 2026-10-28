package refs

import (
	"maps"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// UniqueCommits is what refs sweep reads to tell a contained ref, which it
// may delete, from one holding work nothing else has, and what refs purge
// says a purge loses. Every tip counts all the commits only it reaches, even
// ones another tip shares, a merge counts both sides, and a tip keep reaches
// counts none. It lives here, not in refpins, because it runs git, and
// refpins' own tests run none.
func TestUniqueCommitsCountsWhatOnlyEachTipReaches(t *testing.T) {
	t.Parallel()
	dir := gittest.NewRepo(t, t.TempDir(), "r")
	commit := func(msg string) string {
		gittest.Git(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
		return gittest.Git(t, dir, "rev-parse", "HEAD")
	}
	initial := gittest.Git(t, dir, "rev-parse", "HEAD")
	a := commit("a")
	gittest.Git(t, dir, "switch", "-q", "-c", "x")
	commit("b")
	c := commit("c")
	gittest.Git(t, dir, "switch", "-q", "-c", "y")
	d := commit("d")
	gittest.Git(t, dir, "switch", "-q", "-c", "z", a)
	commit("e")
	gittest.Git(t, dir, "merge", "-q", "--no-ff", "--no-edit", "x")
	m := gittest.Git(t, dir, "rev-parse", "HEAD")

	got, err := refpins.UniqueCommits(dir, []string{c, d, m, a, initial, c}, []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		c:       2, // b, c
		d:       3, // b, c, d: shared with c, counted for each
		m:       4, // m, e, and b, c from the merged side
		a:       0, // main itself
		initial: 0, // behind main
	}
	if !maps.Equal(got, want) {
		t.Errorf("against main: got %v, want %v", got, want)
	}

	got, err = refpins.UniqueCommits(dir, []string{d, m}, []string{"main", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{d: 0, m: 2}; !maps.Equal(got, want) {
		t.Errorf("against main and y: got %v, want %v", got, want)
	}

	got, err = refpins.UniqueCommits(dir, []string{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{a: 2}; !maps.Equal(got, want) {
		t.Errorf("against nothing: got %v, want %v", got, want)
	}
}
