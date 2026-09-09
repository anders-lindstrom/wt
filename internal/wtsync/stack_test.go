package wtsync

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// stackRepo: main; p (1 commit on main); c (1 more on p); d (1 more on c); lone (1 on main).
func stackRepo(t *testing.T) (string, []repo.Worktree) {
	t.Helper()
	dir := linearRepo(t, nil, nil)
	add := func(branch, from, file string) repo.Worktree {
		gitIn(t, dir, "branch", branch, from)
		path := dir + "-" + branch
		gitIn(t, dir, "worktree", "add", "-q", path, branch)
		if err := os.WriteFile(filepath.Join(path, file), []byte(file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, path, "add", "-A")
		gitIn(t, path, "commit", "-q", "-m", file)
		return repo.Worktree{Path: path, Branch: branch}
	}
	p := add("p", "main", "p.txt")
	c := add("c", "p", "c.txt")
	d := add("d", "c", "d.txt")
	lone := add("lone", "main", "lone.txt")
	return dir, []repo.Worktree{p, c, d, lone, {Path: dir, Branch: "main", IsMain: true}, {Path: dir + "-x", Detached: true}}
}

func TestParentsFindsTheNearestAncestorAmongWorktrees(t *testing.T) {
	dir, wts := stackRepo(t)
	got, amb, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"c": "p", "d": "c"}
	if !reflect.DeepEqual(got, want) || len(amb) != 0 {
		t.Fatalf("parents %v ambiguous %v, want %v", got, amb, want)
	}
}

func TestParentsReportsAMergeOfTwoBranchesAsAmbiguous(t *testing.T) {
	dir, wts := stackRepo(t)
	// m merges lone and p, which are incomparable.
	gitIn(t, dir, "branch", "m", "lone")
	path := dir + "-m"
	gitIn(t, dir, "worktree", "add", "-q", path, "m")
	gitIn(t, path, "merge", "-q", "--no-edit", "--no-gpg-sign", "p")
	wts = append(wts, repo.Worktree{Path: path, Branch: "m"})
	got, amb, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["m"]; ok {
		t.Fatalf("m got a parent: %v", got)
	}
	if !reflect.DeepEqual(amb["m"], []string{"lone", "p"}) {
		t.Fatalf("ambiguous %v", amb)
	}
}

func TestParentsIgnoresABranchAtTheSameCommit(t *testing.T) {
	dir, wts := stackRepo(t)
	gitIn(t, dir, "branch", "twin", "p")
	gitIn(t, dir, "worktree", "add", "-q", dir+"-twin", "twin")
	wts = append(wts, repo.Worktree{Path: dir + "-twin", Branch: "twin"})
	got, amb, err := Parents(dir, wts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["twin"]; ok {
		t.Fatalf("twin got a parent: %v", got)
	}
	if got["p"] != "" {
		t.Fatalf("p got a parent: %v", got)
	}
	// c's parent is p, not twin: among candidates at the same tip the
	// lexically smaller name wins, and the shape is not ambiguous.
	if got["c"] != "p" || len(amb) != 0 {
		t.Fatalf("c's parent %q ambiguous %v", got["c"], amb)
	}
}

func TestMembersAndOrder(t *testing.T) {
	parents := map[string]string{"c": "p", "d": "c", "y": "x"}
	if got := Members(parents, "c"); !reflect.DeepEqual(got, []string{"p", "c", "d"}) {
		t.Fatalf("members of c: %v", got)
	}
	if got := Members(parents, "lone"); !reflect.DeepEqual(got, []string{"lone"}) {
		t.Fatalf("members of lone: %v", got)
	}
	got := Order(parents, []string{"d", "lone", "y", "c", "x", "p"})
	pos := map[string]int{}
	for i, b := range got {
		pos[b] = i
	}
	if len(got) != 6 || pos["p"] > pos["c"] || pos["c"] > pos["d"] || pos["x"] > pos["y"] {
		t.Fatalf("order %v", got)
	}
}

func TestDescendantsReturnsOneSubtreeNotTheWholeStack(t *testing.T) {
	// root -> a, b; a -> a1. A failure of a must not reach b or root.
	parents := map[string]string{"a": "root", "b": "root", "a1": "a"}
	for _, tc := range []struct {
		branch string
		want   []string
	}{
		{"root", []string{"a", "a1", "b"}},
		{"a", []string{"a1"}},
		{"b", nil},
		{"a1", nil},
	} {
		got := Descendants(parents, tc.branch)
		if len(got) != len(tc.want) {
			t.Fatalf("Descendants(%q) = %v, want %v", tc.branch, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Descendants(%q) = %v, want %v", tc.branch, got, tc.want)
			}
		}
	}
}
