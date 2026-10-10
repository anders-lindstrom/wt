package repo

import (
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// pushedRepo is a repository with a bare origin and a branch old, pushed
// with its upstream set.
func pushedRepo(t *testing.T) *Repo {
	t.Helper()
	dir := gittest.NewRepo(t, t.TempDir(), "repo")
	gittest.WithOrigin(t, dir)
	gittest.Git(t, dir, "branch", "old", "main")
	gittest.Git(t, dir, "push", "-q", "-u", "origin", "old")
	return &Repo{MainRoot: dir}
}

func recorded(t *testing.T, r *Repo, branch string) string {
	t.Helper()
	to, ok, err := r.PushTo(branch)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return to.String()
}

// A recorded name ends up on the right of a refspec: only a plain branch
// name may get there.
func TestPushToNameTakesOnlyAPlainBranchName(t *testing.T) {
	r := pushedRepo(t)
	for _, name := range []string{"x", "feat/x", "feat_wt/own_apikey", "v1.2", "a@b"} {
		if !r.PushToName(name) {
			t.Errorf("%q refused", name)
		}
	}
	for _, name := range []string{"HEAD", "@", "+main", "-pr", "refs/heads/main", "refs/tags/v1", "x..y", "a b", "@{-1}", "x.lock", ".x", "x/", "", "main:x", ":main", "main^{}", "x~1"} {
		if r.PushToName(name) {
			t.Errorf("%q taken for a branch name", name)
		}
	}
}

func TestPushToIsReadWrittenAndForgotten(t *testing.T) {
	r := pushedRepo(t)
	if recorded(t, r, "old") != "" {
		t.Fatal("recorded before anything was")
	}
	if err := r.UnsetPushTo("old"); err != nil {
		t.Fatalf("forgetting nothing: %v", err)
	}
	if err := r.SetPushTo("old", PushTo{Remote: "origin", Branch: "team/x"}); err != nil {
		t.Fatal(err)
	}
	if got := gittest.Git(t, r.MainRoot, "config", "--get", "branch.old.wtPushTo"); got != "origin team/x" {
		t.Fatalf("written as %q", got)
	}
	if recorded(t, r, "old") != "origin/team/x" {
		t.Fatalf("read %q", recorded(t, r, "old"))
	}
	gittest.Git(t, r.MainRoot, "config", "branch.old.wtPushTo", "origin/team/x")
	if _, _, err := r.PushTo("old"); err == nil {
		t.Fatal("a value with no remote and branch read as one")
	}
	if err := r.UnsetPushTo("old"); err != nil || recorded(t, r, "old") != "" {
		t.Fatalf("err %v, recorded %q", err, recorded(t, r, "old"))
	}
}
