package wtsync

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/repo"
)

func (f ownFixture) own(t *testing.T) *Own {
	t.Helper()
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	return own
}

// on is the commit a bare remote has branch at, "" when it has none.
func on(t *testing.T, bare, branch string) string {
	t.Helper()
	out, err := gittest.Try(t, bare, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// theirs puts a branch on origin from the second clone, one commit on main,
// and fetches it here: a remote branch with no local branch of its name.
func (f ownFixture) theirs(t *testing.T, name string) string {
	t.Helper()
	gittest.Git(t, f.other, "checkout", "-q", "-B", name, "origin/main")
	sha := f.commit(t, f.other, name+".txt", name+"\n", "theirs "+name)
	gittest.Git(t, f.other, "push", "-q", "origin", name)
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	return sha
}

// mine cuts a local branch from a remote one, tracking it, with one commit
// of its own: what git checkout -b mine origin/theirs leaves.
func (f ownFixture) mine(t *testing.T, name, from string) string {
	t.Helper()
	gittest.Git(t, f.dir, "checkout", "-q", "-b", name, "--track", from)
	return f.commit(t, f.dir, name+".txt", name+"\n", "mine "+name)
}

func TestPushOfABranchUnderItsOwnNameGoesWhereItAlwaysDid(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "branch", "local-only", "main")
	own := f.own(t)
	p := own.Push("feat")
	if p.Ref() != "origin/feat" || p.Renamed() || p.SetUpstream || p.Rule != "its own name" {
		t.Fatalf("pushed with an upstream of its own name: %+v", p)
	}
	if got, want := strings.Join(p.Args(), " "), "push --force-with-lease --force-if-includes origin refs/heads/feat:refs/heads/feat"; got != want {
		t.Fatalf("args %q", got)
	}
	p = own.Push("local-only")
	if p.Ref() != "origin/local-only" || p.Renamed() || !p.SetUpstream || p.Rule != "its own name" {
		t.Fatalf("no upstream: %+v", p)
	}
	if got, want := strings.Join(p.Args(), " "), "push --force-with-lease --force-if-includes -u origin refs/heads/local-only:refs/heads/local-only"; got != want {
		t.Fatalf("args %q", got)
	}
	gittest.Git(t, f.dir, p.Args()...)
	if on(t, f.origin, "local-only") == "" {
		t.Fatal("the first push did not make the branch")
	}
	if up := gittest.Git(t, f.dir, "rev-parse", "--abbrev-ref", "local-only@{upstream}"); up != "origin/local-only" {
		t.Fatalf("upstream %q", up)
	}
}

// An upstream that is trunk, a local branch, or the remote branch of another
// local branch is not the branch's own: it pushes under its own name, and
// nothing is asked.
func TestPushIsNeverToAnUpstreamThatIsSomeoneElses(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "branch", "--track", "stats", "origin/main")
	gittest.Git(t, f.dir, "branch", "--track", "on-local", "feat")
	// A stack child cut from its parent's remote branch.
	gittest.Git(t, f.dir, "branch", "--track", "child", "origin/feat")
	// And one cut from a remote branch that another branch is recorded as
	// pushing to, as a renamed parent is.
	f.theirs(t, "old-parent")
	gittest.Git(t, f.dir, "branch", "--no-track", "parent", "origin/old-parent")
	gittest.Git(t, f.dir, "config", "branch.parent.wtPushTo", "origin old-parent")
	gittest.Git(t, f.dir, "branch", "--track", "grandchild", "origin/old-parent")
	for _, mode := range []string{"", "upstream", "current", "simple"} {
		if mode != "" {
			gittest.Git(t, f.dir, "config", "push.default", mode)
		}
		own := f.own(t)
		for _, b := range []string{"stats", "on-local", "child", "grandchild"} {
			p := own.Push(b)
			if p.Ref() != "origin/"+b || p.Undecided != nil || p.Why != "" {
				t.Errorf("push.default=%q %s: %+v", mode, b, p)
			}
			if o := own.State(b); o.State != OwnNone || o.NoPush != "" {
				t.Errorf("push.default=%q %s: %+v", mode, b, o)
			}
		}
	}
	gittest.Git(t, f.dir, "config", "--unset", "push.default")
	feat := on(t, f.origin, "feat")
	gittest.Git(t, f.dir, "checkout", "-q", "child")
	f.commit(t, f.dir, "c.txt", "c\n", "child 1")
	gittest.Git(t, f.dir, f.own(t).Push("child").Args()...)
	if on(t, f.origin, "feat") != feat {
		t.Fatal("the child's push moved its parent's remote branch")
	}
	if on(t, f.origin, "child") != gittest.Git(t, f.dir, "rev-parse", "child") {
		t.Fatal("the child is not on origin under its own name")
	}
	// Trunk is trunk's with no local branch of its name either.
	gittest.Git(t, f.dir, "branch", "-D", "main")
	for _, mode := range []string{"simple", "upstream"} {
		gittest.Git(t, f.dir, "config", "push.default", mode)
		if p := f.own(t).Push("stats"); p.Ref() != "origin/stats" || p.Undecided != nil {
			t.Errorf("no local trunk, push.default=%s: %+v", mode, p)
		}
	}
}

// git itself resolves no push for it under push.default simple, and wt does
// not guess: the branch has nowhere to push until somebody says.
func TestPushOfABranchTrackingAnotherNameIsUndecided(t *testing.T) {
	f := newOwnFixture(t)
	f.theirs(t, "theirs")
	f.mine(t, "mine", "origin/theirs")
	own := f.own(t)
	p := own.Push("mine")
	want := Undecided{Branch: "mine", Upstream: repo.PushTo{Remote: "origin", Branch: "theirs"}, Own: repo.PushTo{Remote: "origin", Branch: "mine"}}
	if p.Remote != "" || p.Undecided == nil || *p.Undecided != want {
		t.Fatalf("%+v", p)
	}
	o := own.State("mine")
	if o.State != OwnUnknown || o.Ref != "" || o.Undecided == nil || o.NoPush != want.Reason() {
		t.Fatalf("%+v", o)
	}
	if got := strings.Join(want.Fix(), " "); got != "wt sync push-to mine origin/theirs" {
		t.Fatalf("fix %q", got)
	}
	if got := own.Asks([]string{"mine"}); len(got) != 0 {
		t.Fatalf("a fetch asks for %v", got)
	}
	// Whatever git is told about pushes: it chooses the remote, and never a
	// branch of another name.
	for _, mode := range []string{"nothing", "upstream", "current", "matching", "simple"} {
		gittest.Git(t, f.dir, "config", "push.default", mode)
		if p := f.own(t).Push("mine"); p.Remote != "" || p.Undecided == nil || *p.Undecided != want {
			t.Fatalf("push.default=%s: %+v", mode, p)
		}
	}
}

func TestARecordedDestinationWinsOverGit(t *testing.T) {
	f := newOwnFixture(t)
	theirs := f.theirs(t, "theirs")
	mine := f.mine(t, "mine", "origin/theirs")
	for _, mode := range []string{"", "current", "upstream"} {
		if mode != "" {
			gittest.Git(t, f.dir, "config", "push.default", mode)
		}
		gittest.Git(t, f.dir, "config", "branch.mine.wtPushTo", "origin mine")
		if p := f.own(t).Push("mine"); p.Ref() != "origin/mine" || p.Rule != "recorded" || p.Renamed() || p.SetUpstream {
			t.Fatalf("push.default=%q, its own name recorded: %+v", mode, p)
		}
		gittest.Git(t, f.dir, "config", "branch.mine.wtPushTo", "origin theirs")
		p := f.own(t).Push("mine")
		if p.Ref() != "origin/theirs" || p.Rule != "recorded" || !p.Renamed() || p.SetUpstream {
			t.Fatalf("push.default=%q, the upstream recorded: %+v", mode, p)
		}
	}
	gittest.Git(t, f.dir, "config", "--unset", "push.default")
	own := f.own(t)
	if o := own.State("mine"); o.State != OwnAhead || o.Ref != "origin/theirs" || o.PushesTo != "origin/theirs" || o.Commit != theirs {
		t.Fatalf("%+v", o)
	}
	if got := own.Asks([]string{"mine"})["origin"]; len(got) != 1 || got[0].Refspec() != "+refs/heads/theirs:refs/remotes/origin/theirs" {
		t.Fatalf("asks %+v", got)
	}
	p := own.Push("mine")
	if got, want := strings.Join(p.Args(), " "), "push --force-with-lease=refs/heads/theirs:"+theirs+" origin refs/heads/mine:refs/heads/theirs"; got != want {
		t.Fatalf("args %q", got)
	}
	gittest.Git(t, f.dir, p.Args()...)
	if on(t, f.origin, "theirs") != mine || on(t, f.origin, "mine") != "" {
		t.Fatalf("theirs %s, mine %q", on(t, f.origin, "theirs"), on(t, f.origin, "mine"))
	}
	if up := gittest.Git(t, f.dir, "rev-parse", "--abbrev-ref", "mine@{upstream}"); up != "origin/theirs" {
		t.Fatalf("the push rewrote the upstream to %q", up)
	}
}

// git's configuration chooses the remote a branch pushes to. It never makes
// a branch of another name the destination: not the upstream under
// push.default upstream, not a remote.<name>.push refspec that renames.
func TestGitsConfigurationChoosesTheRemoteAndNeverAnotherName(t *testing.T) {
	f := newOwnFixture(t)
	fork := filepath.Join(t.TempDir(), "fork.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, fork)
	gittest.Git(t, f.dir, "remote", "add", "fork", fork)
	gittest.Git(t, f.dir, "fetch", "-q", "fork")
	tip := f.commit(t, f.dir, "g.txt", "g\n", "feat 2")

	gittest.Git(t, f.dir, "config", "remote.origin.push", "refs/heads/feat:refs/heads/review/feat")
	if out, _ := gittest.Try(t, f.dir, "push", "--dry-run", "origin"); !strings.Contains(out, "feat -> review/feat") {
		t.Fatalf("the fixture's own git push is meant to go to review/feat:\n%s", out)
	}
	p := f.own(t).Push("feat")
	if p.Ref() != "origin/feat" || p.Renamed() {
		t.Fatalf("a renaming push refspec: %+v", p)
	}
	gittest.Git(t, f.dir, p.Args()...)
	if on(t, f.origin, "feat") != tip || on(t, f.origin, "review/feat") != "" {
		t.Fatalf("feat %s, review/feat %q", on(t, f.origin, "feat"), on(t, f.origin, "review/feat"))
	}
	gittest.Git(t, f.dir, "config", "--unset", "remote.origin.push")

	// The remote is git's to choose.
	gittest.Git(t, f.dir, "config", "remote.pushDefault", "fork")
	if p := f.own(t).Push("feat"); p.Ref() != "fork/feat" {
		t.Fatalf("remote.pushDefault: %+v", p)
	}
	gittest.Git(t, f.dir, "config", "--unset", "remote.pushDefault")
	gittest.Git(t, f.dir, "config", "branch.feat.pushRemote", "fork")
	if p := f.own(t).Push("feat"); p.Ref() != "fork/feat" {
		t.Fatalf("pushRemote: %+v", p)
	}
}

// What the bare remote holds after the push wt makes, under push.default
// upstream: git maps a refspec with no colon to the upstream there, so a
// push meant for the branch's own name went to trunk, to a stack parent, or
// to another branch's remote branch.
func TestPushUnderPushDefaultUpstreamWritesOnlyTheBranchsOwnName(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "config", "push.default", "upstream")
	push := func(branch string) {
		t.Helper()
		p := f.own(t).Push(branch)
		if p.Ref() != "origin/"+branch {
			t.Fatalf("%s pushes to %s", branch, p.Ref())
		}
		gittest.Git(t, f.dir, p.Args()...)
		if got, want := on(t, f.origin, branch), gittest.Git(t, f.dir, "rev-parse", branch); got != want {
			t.Fatalf("origin has %s at %q, the branch is at %s", branch, got, want)
		}
	}
	// Tracking trunk.
	trunk := on(t, f.origin, "main")
	gittest.Git(t, f.dir, "checkout", "-q", "-b", "on-trunk", "--track", "origin/main")
	f.commit(t, f.dir, "x.txt", "x\n", "not for trunk yet")
	push("on-trunk")
	if on(t, f.origin, "main") != trunk {
		t.Fatal("the push moved trunk")
	}
	// A stack child tracking its local parent, which has pushed a commit the
	// child lacks.
	gittest.Git(t, f.dir, "checkout", "-q", "-b", "child", "--track", "feat")
	f.commit(t, f.dir, "c.txt", "c\n", "child 1")
	gittest.Git(t, f.dir, "checkout", "-q", "feat")
	f.commit(t, f.dir, "p2.txt", "p2\n", "parent 2")
	gittest.Git(t, f.dir, "push", "-q", "origin", "refs/heads/feat:refs/heads/feat")
	parent := on(t, f.origin, "feat")
	push("child")
	if on(t, f.origin, "feat") != parent {
		t.Fatal("the child's push replaced its parent's remote branch")
	}
	// Tracking the remote branch of another local branch.
	gittest.Git(t, f.dir, "checkout", "-q", "-b", "beside", "--track", "origin/feat")
	gittest.Git(t, f.dir, "reset", "-q", "--hard", "HEAD~1")
	f.commit(t, f.dir, "b.txt", "b\n", "beside 1")
	push("beside")
	if on(t, f.origin, "feat") != parent {
		t.Fatal("the push replaced another branch's remote branch")
	}
}

// Checked against fork/feat and pushed to origin was the old behaviour: two
// answers to one question.
func TestPushToASameNamedUpstreamOnAnotherRemoteGoesThere(t *testing.T) {
	f := newOwnFixture(t)
	fork := filepath.Join(t.TempDir(), "fork.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, fork)
	gittest.Git(t, f.dir, "remote", "add", "fork", fork)
	gittest.Git(t, f.dir, "fetch", "-q", "fork")
	gittest.Git(t, f.dir, "branch", "-q", "--set-upstream-to=fork/feat", "feat")
	before := on(t, f.origin, "feat")
	tip := f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	own := f.own(t)
	p := own.Push("feat")
	if p.Ref() != "fork/feat" || p.Renamed() || p.SetUpstream {
		t.Fatalf("%+v", p)
	}
	if o := own.State("feat"); o.Ref != "fork/feat" || o.State != OwnAhead || o.Stray != "origin/feat" {
		t.Fatalf("%+v", o)
	}
	gittest.Git(t, f.dir, p.Args()...)
	if on(t, fork, "feat") != tip || on(t, f.origin, "feat") != before {
		t.Fatalf("fork %s, origin %s", on(t, fork, "feat"), on(t, f.origin, "feat"))
	}
	// Another name on another remote is as undecided as on origin.
	gittest.Git(t, f.dir, "push", "-q", "fork", "main:theirs")
	gittest.Git(t, f.dir, "fetch", "-q", "fork")
	f.mine(t, "mine", "fork/theirs")
	p = f.own(t).Push("mine")
	if p.Undecided == nil || p.Undecided.Upstream.String() != "fork/theirs" || p.Undecided.Own.String() != "origin/mine" {
		t.Fatalf("%+v", p)
	}
}

// The remote branch a record names was deleted. The branch still pushes
// there and nowhere else: the push makes it again under the recorded name.
func TestARecordedDestinationWhoseRemoteBranchIsGone(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "branch", "-m", "feat", "renamed")
	gittest.Git(t, f.dir, "config", "branch.renamed.wtPushTo", "origin feat")
	gittest.Git(t, f.dir, "push", "-q", "origin", "--delete", "feat")
	gittest.Git(t, f.dir, "fetch", "-q", "--prune", "origin")
	own := f.own(t)
	if o := own.State("renamed"); o.State != OwnGone || o.Ref != "origin/feat" {
		t.Fatalf("%+v", o)
	}
	p := own.Push("renamed")
	if p.Ref() != "origin/feat" || p.SetUpstream {
		t.Fatalf("%+v", p)
	}
	gittest.Git(t, f.dir, p.Args()...)
	if on(t, f.origin, "feat") != gittest.Git(t, f.dir, "rev-parse", "renamed") || on(t, f.origin, "renamed") != "" {
		t.Fatalf("feat %q, renamed %q", on(t, f.origin, "feat"), on(t, f.origin, "renamed"))
	}
	// Recorded for a branch that never had it as its upstream, and not there
	// yet: nothing to check, and the push makes it.
	gittest.Git(t, f.dir, "branch", "fresh", "main")
	gittest.Git(t, f.dir, "config", "branch.fresh.wtPushTo", "origin elsewhere")
	own = f.own(t)
	if o := own.State("fresh"); o.State != OwnNone {
		t.Fatalf("%+v", o)
	}
	if p := own.Push("fresh"); p.Ref() != "origin/elsewhere" || !p.SetUpstream {
		t.Fatalf("%+v", p)
	}
}

func TestARecordedDestinationThatCannotBeUsedLeavesNowhereToPush(t *testing.T) {
	f := newOwnFixture(t)
	for value, want := range map[string]string{
		"origin":                 "is not a remote and a branch",
		"nowhere feat":           "there is no remote nowhere",
		"origin main":            "which is trunk",
		"origin a b":             "is not a remote and a branch",
		"origin/feat":            "is not a remote and a branch",
		"origin  feat x":         "is not a remote and a branch",
		"origin HEAD":            `"HEAD" is not a branch name`,
		"origin @":               `"@" is not a branch name`,
		"origin +feat":           `"+feat" is not a branch name`,
		"origin -pr":             `"-pr" is not a branch name`,
		"origin refs/heads/feat": `"refs/heads/feat" is not a branch name`,
		"origin x..y":            `"x..y" is not a branch name`,
	} {
		gittest.Git(t, f.dir, "config", "branch.feat.wtPushTo", value)
		own := f.own(t)
		p := own.Push("feat")
		if p.Remote != "" || p.Undecided != nil || !strings.Contains(p.Why, want) {
			t.Errorf("%q: %+v", value, p)
		}
		if o := own.State("feat"); o.State != OwnUnknown || o.NoPush != p.Why {
			t.Errorf("%q: %+v", value, o)
		}
	}
}

// renamed is the fixture's feat under another name, recorded as pushing to
// origin/feat: what a branch wt migrate renamed looks like.
func (f ownFixture) renamed(t *testing.T) {
	t.Helper()
	gittest.Git(t, f.dir, "branch", "-m", "feat", "renamed")
	gittest.Git(t, f.dir, "config", "branch.renamed.wtPushTo", "origin feat")
}

// With the names matching, the lease is the remote-tracking ref and
// --force-if-includes refuses a commit a fetch brought that the branch never
// had. Naming an expected commit in the lease switches that check off.
func TestALeaseWithAnExpectedCommitWouldPushOverAFetchedCommit(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "commit", "-q", "--amend", "-m", "feat 1, rewritten")
	theirs := f.pushFromOther(t, "feat", "t.txt", "t\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	p := f.own(t).Push("feat")
	if out, err := gittest.Try(t, f.dir, p.Args()...); err == nil || !strings.Contains(out, "remote ref updated since checkout") {
		t.Fatalf("pushed over a commit the branch never had: %v\n%s", err, out)
	}
	if on(t, f.origin, "feat") != theirs {
		t.Fatal("origin lost their commit")
	}
	if out, err := gittest.Try(t, f.dir, "push", "--dry-run", "--force-with-lease=refs/heads/feat:"+theirs, "--force-if-includes", "origin", "refs/heads/feat:refs/heads/feat"); err != nil || !strings.Contains(out, "forced update") {
		t.Fatalf("with an expected commit git was meant to let it through: %v\n%s", err, out)
	}
}

// With the names differing, --force-if-includes reads the reflog of a local
// branch named like the remote one. There is none, so it refuses every push,
// a fast-forward of commits the branch has always had included. That is why
// a destination of another name is not pushed with it.
func TestForceIfIncludesRefusesEveryPushToABranchOfAnotherName(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	before := on(t, f.origin, "feat")
	out, err := gittest.Try(t, f.dir, "push", "--force-with-lease=refs/heads/feat", "--force-if-includes", "origin", "refs/heads/renamed:refs/heads/feat")
	if err == nil || !strings.Contains(out, "remote ref updated since checkout") || on(t, f.origin, "feat") != before {
		t.Fatalf("git now takes a fast-forward under another name with --force-if-includes; wt could use it: %v\n%s", err, out)
	}
	p := f.own(t).Push("renamed")
	if slices.Contains(p.Args(), "--force-if-includes") {
		t.Fatalf("args %v", p.Args())
	}
	gittest.Git(t, f.dir, p.Args()...)
	if on(t, f.origin, "feat") != gittest.Git(t, f.dir, "rev-parse", "renamed") {
		t.Fatal("the fast-forward did not reach origin")
	}
}

// The remote branch got a commit this branch never had, and a fetch brought
// it: the lease alone would pass, so Push refuses.
func TestPushUnderAnotherNameRefusesAFetchedCommitTheBranchNeverHad(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	gittest.Git(t, f.dir, "commit", "-q", "--amend", "-m", "feat 1, rewritten")
	f.pushFromOther(t, "feat", "t.txt", "t\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	p := f.own(t).Push("renamed")
	if p.Remote != "" || p.Why != "origin/feat has commits this branch never had" || p.Undecided != nil {
		t.Fatalf("%+v", p)
	}
}

// The same commit, not fetched: wt compares with what it last saw and
// pushes, and the commit it pinned in the lease makes git refuse.
func TestPushUnderAnotherNamePinsTheCommitItCompared(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	seen := on(t, f.origin, "feat")
	gittest.Git(t, f.dir, "commit", "-q", "--amend", "-m", "feat 1, rewritten")
	theirs := f.pushFromOther(t, "feat", "t.txt", "t\n")
	p := f.own(t).Push("renamed")
	if p.Expected != seen {
		t.Fatalf("expected %q, the commit compared is %s", p.Expected, seen)
	}
	if out, err := gittest.Try(t, f.dir, p.Args()...); err == nil || !strings.Contains(out, "stale info") {
		t.Fatalf("pushed over a commit never fetched: %v\n%s", err, out)
	}
	if on(t, f.origin, "feat") != theirs {
		t.Fatal("origin lost their commit")
	}
	// A fetch between the comparison and the push changes nothing: the
	// lease is the commit compared, not whatever the tracking ref says now.
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if out, err := gittest.Try(t, f.dir, p.Args()...); err == nil || !strings.Contains(out, "stale info") {
		t.Fatalf("a fetch after the comparison let the push through: %v\n%s", err, out)
	}
	if on(t, f.origin, "feat") != theirs {
		t.Fatal("origin lost their commit")
	}
}

// What the remote has is an older version of the branch's own work, rebased
// several times since: it pushes.
func TestPushUnderAnotherNameReplacesOlderVersionsOfTheBranch(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	gittest.Git(t, f.dir, "checkout", "-q", "main")
	f.commit(t, f.dir, "m1.txt", "m1\n", "trunk 1")
	gittest.Git(t, f.dir, "checkout", "-q", "renamed")
	gittest.Git(t, f.dir, "rebase", "-q", "main")
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	gittest.Git(t, f.dir, "checkout", "-q", "main")
	f.commit(t, f.dir, "m2.txt", "m2\n", "trunk 2")
	gittest.Git(t, f.dir, "checkout", "-q", "renamed")
	gittest.Git(t, f.dir, "rebase", "-q", "main")
	own := f.own(t)
	if o := own.State("renamed"); o.State != OwnRebased {
		t.Fatalf("%+v", o)
	}
	p := own.Push("renamed")
	if p.Expected != on(t, f.origin, "feat") {
		t.Fatalf("%+v", p)
	}
	gittest.Git(t, f.dir, p.Args()...)
	if on(t, f.origin, "feat") != gittest.Git(t, f.dir, "rev-parse", "renamed") || on(t, f.origin, "renamed") != "" {
		t.Fatalf("feat %q, renamed %q", on(t, f.origin, "feat"), on(t, f.origin, "renamed"))
	}
}

// What shows the remote holds an old version of this branch is the commit
// itself: in the branch's reflog, or under a ref a run pinned. With neither,
// a remote whose commits only match patch for patch is not pushed over,
// though it still reads rebased.
func TestPushUnderAnotherNameNeedsAFormerTipNotTheSamePatches(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	old := on(t, f.origin, "feat")
	gittest.Git(t, f.dir, "checkout", "-q", "main")
	f.commit(t, f.dir, "m1.txt", "m1\n", "trunk 1")
	gittest.Git(t, f.dir, "checkout", "-q", "renamed")
	gittest.Git(t, f.dir, "rebase", "-q", "main")
	if p := f.own(t).Push("renamed"); p.Expected != old {
		t.Fatalf("with its reflog: %+v", p)
	}
	gittest.Git(t, f.dir, "reflog", "expire", "--expire=now", "--expire-unreachable=now", "--all")
	own := f.own(t)
	if o := own.State("renamed"); o.State != OwnRebased || o.NoPush == "" {
		t.Fatalf("the same patch on a new base, and nothing remembering the old commit: %+v", o)
	}
	const why = "origin/feat is at a commit this branch was never at, so it cannot be shown to hold only old versions of this branch"
	if p := own.Push("renamed"); p.Remote != "" || p.Why != why {
		t.Fatalf("%+v", p)
	}
	// A ref a run pinned says it as well as the reflog did.
	gittest.Git(t, f.dir, "update-ref", SafetyPrefix+"renamed/1", old)
	if p := f.own(t).Push("renamed"); p.Expected != old {
		t.Fatalf("with a pinned ref: %+v", p)
	}
}

// A teammate amended this branch's commit on the remote: another message, or
// other whitespace, and the same patch. Their commit is one this branch
// never had, whatever its patch says.
func TestPushUnderAnotherNameRefusesSomebodyElsesCommitWithTheSamePatch(t *testing.T) {
	for name, amend := range map[string]func(f ownFixture){
		"message": func(f ownFixture) {
			gittest.Git(t, f.other, "commit", "-q", "--amend", "-m", "feat 1 (a teammate reworded it)")
		},
		"whitespace": func(f ownFixture) {
			gittest.WriteFile(t, filepath.Join(f.other, "f.txt"), "        f\n")
			gittest.Git(t, f.other, "commit", "-q", "-a", "--amend", "--no-edit")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newOwnFixture(t)
			f.renamed(t)
			gittest.Git(t, f.other, "fetch", "-q", "origin")
			gittest.Git(t, f.other, "checkout", "-q", "-B", "feat", "origin/feat")
			amend(f)
			gittest.Git(t, f.other, "push", "-q", "--force", "origin", "feat")
			theirs := on(t, f.origin, "feat")
			gittest.Git(t, f.dir, "fetch", "-q", "origin")
			gittest.Git(t, f.dir, "checkout", "-q", "main")
			f.commit(t, f.dir, "m1.txt", "m1\n", "trunk 1")
			gittest.Git(t, f.dir, "checkout", "-q", "renamed")
			gittest.Git(t, f.dir, "rebase", "-q", "main")
			own := f.own(t)
			if o := own.State("renamed"); o.State != OwnRebased {
				t.Fatalf("the fixture is meant to read rebased, by the patches: %+v", o)
			}
			if p := own.Push("renamed"); p.Remote != "" {
				t.Fatalf("wt would push over a teammate's commit: %v", p.Args())
			}
			if on(t, f.origin, "feat") != theirs {
				t.Fatal("origin lost their commit")
			}
		})
	}
}

// Where the branch cannot be compared with the remote, under another name
// there is no push: the lease would be a commit nothing was checked against.
func TestPushUnderAnotherNameIsNotMadeUnchecked(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, elsewhere)
	gittest.Git(t, f.dir, "remote", "set-url", "--push", "origin", elsewhere)
	own := f.own(t)
	o := own.State("renamed")
	if o.State != OwnUnknown || o.NoPush != "it cannot be compared with origin/feat: origin pushes to another URL than it fetches from" {
		t.Fatalf("%+v", o)
	}
	if p := own.Push("renamed"); p.Remote != "" || p.Why != o.NoPush {
		t.Fatalf("%+v", p)
	}
	// Under its own name the same remote is pushed to as it always was,
	// unchecked and said so.
	gittest.Git(t, f.dir, "config", "--unset", "branch.renamed.wtPushTo")
	gittest.Git(t, f.dir, "branch", "-m", "renamed", "feat")
	own = f.own(t)
	if o := own.State("feat"); o.State != OwnUnknown || o.NoPush != "" {
		t.Fatalf("%+v", o)
	}
	if p := own.Push("feat"); p.Ref() != "origin/feat" {
		t.Fatalf("%+v", p)
	}
}

// Trunk itself has nowhere to push, checked out in a worktree or not, and
// whatever is recorded for it.
func TestTrunkHasNowhereToPush(t *testing.T) {
	f := newOwnFixture(t)
	own := f.own(t)
	if p := own.Push("main"); p.Remote != "" || p.Why == "" {
		t.Fatalf("%+v %v", p, p.Args())
	}
	if o := own.State("main"); o.State != OwnNone || o.Ref != "" {
		t.Fatalf("%+v", o)
	}
	if got := own.Asks([]string{"main"}); len(got) != 0 {
		t.Fatalf("asks %+v", got)
	}
}

// Somebody force-moved the remote branch to history that has nothing to do
// with this branch.
func TestPushUnderAnotherNameRefusesUnrelatedHistory(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	gittest.Git(t, f.other, "checkout", "-q", "--orphan", "unrelated")
	gittest.Git(t, f.other, "rm", "-q", "-rf", ".")
	f.commit(t, f.other, "u.txt", "u\n", "unrelated")
	gittest.Git(t, f.other, "push", "-q", "--force", "origin", "unrelated:feat")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if _, err := gittest.Try(t, f.dir, "merge-base", "renamed", "origin/feat"); err == nil {
		t.Fatal("the fixture's histories share a commit")
	}
	p := f.own(t).Push("renamed")
	if p.Remote != "" || p.Why != "origin/feat has commits this branch never had" {
		t.Fatalf("%+v", p)
	}
}

// The remote has the branch and nothing here says so: the push says "must
// not exist", and git refuses.
func TestPushUnderAnotherNameWithNoTrackingRefMustNotFindTheBranch(t *testing.T) {
	f := newOwnFixture(t)
	f.renamed(t)
	before := on(t, f.origin, "feat")
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	gittest.Git(t, f.dir, "update-ref", "-d", "refs/remotes/origin/feat")
	p := f.own(t).Push("renamed")
	if p.Remote == "" || p.Expected != "" || !slices.Contains(p.Args(), "--force-with-lease=refs/heads/feat:") {
		t.Fatalf("%+v %v", p, p.Args())
	}
	if out, err := gittest.Try(t, f.dir, p.Args()...); err == nil || !strings.Contains(out, "stale info") {
		t.Fatalf("pushed over a branch it did not know of: %v\n%s", err, out)
	}
	if on(t, f.origin, "feat") != before {
		t.Fatal("origin's branch moved")
	}
}

// Both exist after the pushes of a renamed branch went to its new name while
// its pull request stayed on the old one.
func TestOwnRemoteNamesAStrayBranchOfItsOwnName(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "branch", "-m", "feat", "renamed")
	gittest.Git(t, f.dir, "push", "-q", "origin", "renamed")
	if o := f.state(t, "renamed"); o.State != OwnUnknown || o.Undecided == nil || o.Stray != "origin/renamed" {
		t.Fatalf("undecided: %+v", o)
	}
	gittest.Git(t, f.dir, "config", "branch.renamed.wtPushTo", "origin feat")
	if o := f.state(t, "renamed"); o.State != OwnInSync || o.Ref != "origin/feat" || o.Stray != "origin/renamed" || o.PushesTo != "origin/feat" {
		t.Fatalf("recorded: %+v", o)
	}
	gittest.Git(t, f.dir, "config", "branch.renamed.wtPushTo", "origin renamed")
	if o := f.state(t, "renamed"); o.Ref != "origin/renamed" || o.Stray != "" || o.PushesTo != "" {
		t.Fatalf("its own name recorded: %+v", o)
	}
}
