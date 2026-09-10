package repo

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sweepGit runs git in dir and returns trimmed stdout, failing the test on error.
func sweepGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// sweepFixture is a repository on main with one commit and a bare origin, fetched.
func sweepFixture(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	sweepGit(t, filepath.Dir(dir), "init", "-q", "-b", "main", dir)
	sweepGit(t, dir, "config", "user.email", "t@example.com")
	sweepGit(t, dir, "config", "user.name", "T")
	sweepGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	origin := filepath.Join(t.TempDir(), "origin.git")
	sweepGit(t, dir, "clone", "-q", "--bare", dir, origin)
	sweepGit(t, dir, "remote", "add", "origin", origin)
	sweepGit(t, dir, "fetch", "-q", "origin")
	r, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func TestBranchesReportsTipUpstreamAndGone(t *testing.T) {
	r, dir := sweepFixture(t)
	sweepGit(t, dir, "branch", "old-idea")
	sweepGit(t, dir, "config", "branch.old-idea.remote", "origin")
	sweepGit(t, dir, "config", "branch.old-idea.merge", "refs/heads/old-idea")
	// A tag with the same name is what makes refname:short print heads/...
	sweepGit(t, dir, "tag", "old-idea")

	got, err := r.Branches()
	if err != nil {
		t.Fatal(err)
	}
	var idea *Branch
	for i := range got {
		if got[i].Name == "old-idea" {
			idea = &got[i]
		}
	}
	if idea == nil {
		t.Fatalf("old-idea not listed by its exact name: %+v", got)
	}
	if idea.Tip != sweepGit(t, dir, "rev-parse", "main") {
		t.Errorf("tip = %q", idea.Tip)
	}
	if idea.Upstream != "refs/remotes/origin/old-idea" || !idea.Gone {
		t.Errorf("want a configured, gone upstream: %+v", *idea)
	}
	if idea.Subject != "init" || idea.Date == "" {
		t.Errorf("want the tip's subject and date: %+v", *idea)
	}
}

func TestBranchesWithoutUpstreamIsNotGone(t *testing.T) {
	r, dir := sweepFixture(t)
	sweepGit(t, dir, "branch", "wip")
	got, err := r.Branches()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range got {
		if b.Name == "wip" && (b.Gone || b.Upstream != "") {
			t.Errorf("a branch with no upstream is not gone: %+v", b)
		}
	}
}

func TestMergedIntoListsBranchesReachableFromACommit(t *testing.T) {
	r, dir := sweepFixture(t)
	sweepGit(t, dir, "branch", "done-work")
	tree := sweepGit(t, dir, "rev-parse", "main^{tree}")
	ahead := sweepGit(t, dir, "commit-tree", "-p", "main", "-m", "wip work", tree)
	sweepGit(t, dir, "branch", "wip", ahead)

	got, err := r.MergedInto(sweepGit(t, dir, "rev-parse", "main"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "done-work") || !slices.Contains(got, "main") {
		t.Errorf("want done-work and main: %v", got)
	}
	if slices.Contains(got, "wip") {
		t.Errorf("wip has a commit main lacks: %v", got)
	}
}

func TestResolveRefAndHasRemote(t *testing.T) {
	r, dir := sweepFixture(t)
	tip, ok := r.ResolveRef("refs/remotes/origin/main")
	if !ok || tip != sweepGit(t, dir, "rev-parse", "main") {
		t.Errorf("origin/main = %q, %v", tip, ok)
	}
	if _, ok := r.ResolveRef("refs/remotes/origin/nope"); ok {
		t.Error("a missing ref must not resolve")
	}
	if !r.HasRemote("origin") || r.HasRemote("upstream") {
		t.Error("HasRemote: want origin and not upstream")
	}
}

// A symref whose target was pruned still says which branch is trunk.
func TestOriginHeadReadsADanglingHead(t *testing.T) {
	r, dir := sweepFixture(t)
	sweepGit(t, dir, "remote", "set-head", "origin", "main")
	if got, ok := r.OriginHead(); !ok || got != "main" {
		t.Errorf("OriginHead = %q, %v; want main", got, ok)
	}
	sweepGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/integration")
	if got, ok := r.OriginHead(); !ok || got != "integration" {
		t.Errorf("dangling OriginHead = %q, %v; want integration", got, ok)
	}
	sweepGit(t, dir, "remote", "set-head", "origin", "--delete")
	if _, ok := r.OriginHead(); ok {
		t.Error("no origin/HEAD must report false")
	}
}

// A local ref named origin/<x> makes --short print remotes/origin/<x>.
func TestOriginHeadIsNotConfusedByASameNamedLocalRef(t *testing.T) {
	r, dir := sweepFixture(t)
	sweepGit(t, dir, "remote", "set-head", "origin", "main")
	sweepGit(t, dir, "branch", "origin/main", "main")
	if got, ok := r.OriginHead(); !ok || got != "main" {
		t.Errorf("OriginHead = %q, %v; want main", got, ok)
	}
}

func TestDeleteBranchAtRefusesAMovedBranchAndCleansConfig(t *testing.T) {
	r, dir := sweepFixture(t)
	sweepGit(t, dir, "branch", "done-work")
	sweepGit(t, dir, "config", "branch.done-work.remote", "origin")
	seen := sweepGit(t, dir, "rev-parse", "done-work")
	tree := sweepGit(t, dir, "rev-parse", "main^{tree}")
	moved := sweepGit(t, dir, "commit-tree", "-p", "done-work", "-m", "landed late", tree)
	sweepGit(t, dir, "update-ref", "refs/heads/done-work", moved)

	if err := r.DeleteBranchAt("done-work", seen); err == nil {
		t.Fatal("a branch that moved since it was seen must not be deleted")
	}
	if !r.BranchExists("done-work") {
		t.Fatal("the refused delete removed the branch")
	}
	if err := r.DeleteBranchAt("done-work", moved); err != nil {
		t.Fatalf("DeleteBranchAt at the current tip: %v", err)
	}
	if r.BranchExists("done-work") {
		t.Error("the branch should be gone")
	}
	if out, err := exec.Command("git", "-C", dir, "config", "branch.done-work.remote").Output(); err == nil {
		t.Errorf("branch config should be removed with the branch, still %q", out)
	}
}
