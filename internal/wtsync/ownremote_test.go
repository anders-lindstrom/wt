package wtsync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// ownFixture is a repository on main with a bare origin, a branch feat one
// commit ahead and pushed, and a second clone that can move origin on.
type ownFixture struct {
	dir, origin, other string
}

func newOwnFixture(t *testing.T) ownFixture {
	t.Helper()
	dir := gittest.NewRepo(t, t.TempDir(), "repo")
	gittest.WriteFile(t, filepath.Join(dir, "a.txt"), "a\n")
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-q", "-m", "a")
	origin := gittest.WithOrigin(t, dir)
	gittest.Git(t, dir, "checkout", "-q", "-b", "feat")
	gittest.WriteFile(t, filepath.Join(dir, "f.txt"), "f\n")
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-q", "-m", "feat 1")
	gittest.Git(t, dir, "push", "-q", "-u", "origin", "feat")
	other := filepath.Join(t.TempDir(), "other")
	gittest.Git(t, filepath.Dir(other), "clone", "-q", origin, other)
	gittest.Git(t, other, "config", "user.email", "t@example.com")
	gittest.Git(t, other, "config", "user.name", "T")
	return ownFixture{dir: dir, origin: origin, other: other}
}

// commit adds one commit changing file on the branch checked out in dir.
func (f ownFixture) commit(t *testing.T, dir, file, content, subject string) string {
	t.Helper()
	gittest.WriteFile(t, filepath.Join(dir, file), content)
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-q", "-m", subject)
	return gittest.Git(t, dir, "rev-parse", "HEAD")
}

// pushFromOther commits on branch in the second clone and pushes it.
func (f ownFixture) pushFromOther(t *testing.T, branch, file, content string) string {
	t.Helper()
	gittest.Git(t, f.other, "fetch", "-q", "origin")
	gittest.Git(t, f.other, "checkout", "-q", "-B", branch, "origin/"+branch)
	sha := f.commit(t, f.other, file, content, "theirs "+file)
	gittest.Git(t, f.other, "push", "-q", "origin", branch)
	return sha
}

func (f ownFixture) state(t *testing.T, branch string) OwnRemote {
	t.Helper()
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	return own.State(branch)
}

func TestOwnRemoteIsTheRefAPushReplaces(t *testing.T) {
	f := newOwnFixture(t)
	if o := f.state(t, "feat"); o.State != OwnInSync || o.Ref != "origin/feat" || o.Commit == "" {
		t.Fatalf("pushed and untouched: %+v", o)
	}
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	if o := f.state(t, "feat"); o.State != OwnAhead || o.Ahead != 1 || o.Behind != 0 {
		t.Fatalf("one commit on top: %+v", o)
	}
	// Never pushed, with no upstream: git resolves no push destination, and
	// origin has no ref of that name.
	gittest.Git(t, f.dir, "branch", "local-only", "main")
	if o := f.state(t, "local-only"); o.State != OwnNone || o.Ref != "" {
		t.Fatalf("never pushed: %+v", o)
	}
	if o := f.state(t, "no-such-branch"); o.State != OwnNone {
		t.Fatalf("no branch: %+v", o)
	}
}

// A branch cut from trunk that tracks trunk and has no ref of its own on
// origin has nothing to check, whatever its upstream is ahead by: the
// upstream is not where it pushes.
func TestOwnRemoteIsNotTheUpstream(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "branch", "--track", "stats", "origin/main")
	f.pushFromOther(t, "main", "m.txt", "m\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if o := f.state(t, "stats"); o.State != OwnNone {
		t.Fatalf("tracks trunk, never pushed: %+v", o)
	}
	// Under push.default=upstream its push would land on trunk's own ref:
	// still nothing of its own.
	gittest.Git(t, f.dir, "config", "push.default", "upstream")
	if o := f.state(t, "stats"); o.State != OwnNone {
		t.Fatalf("pushes to trunk: %+v", o)
	}
	gittest.Git(t, f.dir, "config", "--unset", "push.default")
	// Once it has a ref of its own on origin, that ref is compared, not the
	// upstream.
	gittest.Git(t, f.dir, "push", "-q", "origin", "stats")
	if o := f.state(t, "stats"); o.State != OwnInSync || o.Ref != "origin/stats" {
		t.Fatalf("pushed under its own name: %+v", o)
	}
}

func TestOwnRemoteBehindAndDiverged(t *testing.T) {
	f := newOwnFixture(t)
	theirs := f.pushFromOther(t, "feat", "t.txt", "t\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if o := f.state(t, "feat"); o.State != OwnBehind || o.Behind != 1 || o.Ahead != 0 || o.Commit != theirs {
		t.Fatalf("origin moved on: %+v", o)
	}
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	o := f.state(t, "feat")
	if o.State != OwnDiverged || o.Ahead != 1 || o.Behind != 1 {
		t.Fatalf("both moved: %+v", o)
	}
	if !strings.Contains(o.Refusal(), "origin/feat has 1 commit this branch never had") {
		t.Fatalf("refusal: %q", o.Refusal())
	}
}

// Rebased and not pushed is both ahead and behind, and must never read as
// diverged. A rebase that had to change a commit leaves no patch to match,
// so only the tips the branch once had can say so: its reflog, and with no
// reflog the safety ref a run pinned.
func TestOwnRemoteRebasedIsAFormerTip(t *testing.T) {
	f := newOwnFixture(t)
	pushed := gittest.Git(t, f.dir, "rev-parse", "feat")
	// Trunk and the branch both add f.txt, so the rebase stops and is
	// resolved by hand: the replayed commit is a different patch.
	f.pushFromOther(t, "main", "f.txt", "trunk\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if _, err := gittest.Try(t, f.dir, "rebase", "origin/main"); err == nil {
		t.Fatal("the rebase was meant to stop")
	}
	gittest.WriteFile(t, filepath.Join(f.dir, "f.txt"), "both\n")
	gittest.Git(t, f.dir, "add", "-A")
	if out, err := gittest.Try(t, f.dir, "rebase", "--continue"); err != nil {
		t.Fatalf("continue: %v\n%s", err, out)
	}
	o := f.state(t, "feat")
	if o.State != OwnRebased || o.Ahead == 0 || o.Behind != 1 || o.Commit != pushed {
		t.Fatalf("rebased with a resolution: %+v", o)
	}
	// Without the reflog nothing says the remote's tip was ever the
	// branch's, and the patches differ.
	gittest.Git(t, f.dir, "reflog", "expire", "--expire=now", "--all")
	if o := f.state(t, "feat"); o.State != OwnDiverged {
		t.Fatalf("no reflog, no safety ref: %+v", o)
	}
	if _, err := WriteSafety(f.dir, "feat", pushed, 7); err != nil {
		t.Fatal(err)
	}
	if o := f.state(t, "feat"); o.State != OwnRebased {
		t.Fatalf("the safety ref of a run pins the old tip: %+v", o)
	}
}

// A plain rebase replays the same patches, which is enough on its own.
func TestOwnRemoteRebasedIsPatchForPatch(t *testing.T) {
	f := newOwnFixture(t)
	f.pushFromOther(t, "main", "m.txt", "m\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	gittest.Git(t, f.dir, "rebase", "-q", "origin/main")
	gittest.Git(t, f.dir, "reflog", "expire", "--expire=now", "--all")
	if o := f.state(t, "feat"); o.State != OwnRebased {
		t.Fatalf("rebased, same patches, no reflog: %+v", o)
	}
}

// The opposite: a branch that was rebased, whose remote has also gained a
// commit it never had. The old tip the branch once had is not the remote's
// tip any more, and the new commit matches nothing.
func TestOwnRemoteRebasedOverACommitItNeverHadIsDiverged(t *testing.T) {
	f := newOwnFixture(t)
	f.pushFromOther(t, "main", "m.txt", "m\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	gittest.Git(t, f.dir, "rebase", "-q", "origin/main")
	if o := f.state(t, "feat"); o.State != OwnRebased {
		t.Fatalf("before the remote moved: %+v", o)
	}
	f.pushFromOther(t, "feat", "t.txt", "t\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	o := f.state(t, "feat")
	if o.State != OwnDiverged || o.Behind != 2 {
		t.Fatalf("rebased, and the remote has a commit of its own: %+v", o)
	}
}

// Commits that change nothing all carry the same patch, so one on the
// branch's side would match any on the remote's: the patch rule does not
// take them for a rebase.
func TestOwnRemoteEmptyCommitsAreNotARebase(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.other, "fetch", "-q", "origin")
	gittest.Git(t, f.other, "checkout", "-q", "-B", "feat", "origin/feat")
	gittest.Git(t, f.other, "commit", "-q", "--allow-empty", "-m", "theirs, empty")
	gittest.Git(t, f.other, "push", "-q", "origin", "feat")
	gittest.Git(t, f.dir, "commit", "-q", "--allow-empty", "-m", "mine, empty")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if o := f.state(t, "feat"); o.State != OwnDiverged || o.Ahead != 1 || o.Behind != 1 {
		t.Fatalf("%+v", o)
	}
}

// With push.default simple git resolves no @{push} for a branch whose push
// remote is not its upstream's, and pushes it there under its own name:
// that is the ref compared, not origin's.
func TestOwnRemoteFallsBackToThePushRemote(t *testing.T) {
	f := newOwnFixture(t)
	fork := filepath.Join(t.TempDir(), "fork.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, fork)
	gittest.Git(t, f.dir, "remote", "add", "fork", fork)
	gittest.Git(t, f.dir, "fetch", "-q", "fork")
	for _, key := range []string{"branch.feat.pushRemote", "remote.pushDefault"} {
		gittest.Git(t, f.dir, "config", key, "fork")
		if out, err := gittest.Try(t, f.dir, "rev-parse", "--symbolic-full-name", "feat@{push}"); err == nil {
			t.Fatalf("%s: the fixture resolves @{push} to %s", key, out)
		}
		own, err := ReadOwn(f.dir, "main")
		if err != nil {
			t.Fatal(err)
		}
		if o := own.State("feat"); o.State != OwnInSync || o.Ref != "fork/feat" {
			t.Fatalf("%s: %+v", key, o)
		}
		if got := Remotes(own.Asks([]string{"feat"})); len(got) != 1 || got[0] != "fork" {
			t.Fatalf("%s: fetches %v", key, got)
		}
		gittest.Git(t, f.dir, "config", "--unset", key)
	}
	// And with push.default nothing, where git pushes nothing unasked.
	gittest.Git(t, f.dir, "config", "push.default", "nothing")
	if o := f.state(t, "feat"); o.State != OwnInSync || o.Ref != "origin/feat" {
		t.Fatalf("push.default nothing: %+v", o)
	}
}

// A remote whose name continues another's does not claim that one's refs:
// origin/team is not where origin's team/x is pushed.
func TestOwnRemoteIsNotARemoteWhoseNameOverlaps(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "checkout", "-q", "-b", "team/x")
	gittest.Git(t, f.dir, "push", "-q", "-u", "origin", "team/x")
	other := filepath.Join(t.TempDir(), "team.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, other)
	// git remote add refuses the name; the configuration takes it.
	gittest.Git(t, f.dir, "config", "remote.origin/team.url", other)
	gittest.Git(t, f.dir, "config", "remote.origin/team.fetch", "+refs/heads/*:refs/remotes/origin/team/*")
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	asks := own.Asks([]string{"team/x"})
	if got := asks["origin"]; len(got) != 1 || got[0].Refspec() != "+refs/heads/team/x:refs/remotes/origin/team/x" || len(asks) != 1 {
		t.Fatalf("asks %v", asks)
	}
}

func TestOwnRemoteGone(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.origin, "update-ref", "-d", "refs/heads/feat")
	// As last fetched the tracking ref is still there and says in sync.
	if o := f.state(t, "feat"); o.State != OwnInSync {
		t.Fatalf("before any fetch: %+v", o)
	}
	// A fetch that asked the remote for it and was told it is not there
	// knows better, and leaves the tracking ref alone.
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	asks := own.Asks([]string{"feat"})
	if got := asks["origin"]; len(got) != 1 || got[0].Refspec() != "+refs/heads/feat:refs/remotes/origin/feat" {
		t.Fatalf("asks: %+v", asks)
	}
	own.Gone(asks["origin"][0])
	if err := own.Refresh(); err != nil {
		t.Fatal(err)
	}
	if o := own.State("feat"); o.State != OwnGone || o.Ref != "origin/feat" || o.Commit != "" || !o.Fetched {
		t.Fatalf("gone from the remote: %+v", o)
	}
	if gittest.Git(t, f.dir, "rev-parse", "--verify", "refs/remotes/origin/feat") == "" {
		t.Fatal("the tracking ref was removed")
	}
	// Pruned, the branch still names it as its upstream: gone, not none.
	gittest.Git(t, f.dir, "fetch", "-q", "--prune", "origin")
	if o := f.state(t, "feat"); o.State != OwnGone {
		t.Fatalf("pruned: %+v", o)
	}
}

// A ref this command fetched says so, and one it could not fetch says why
// and keeps a run off: the rest of it is then as last fetched.
func TestOwnRemoteRecordsWhatAFetchDid(t *testing.T) {
	f := newOwnFixture(t)
	theirs := f.pushFromOther(t, "feat", "t.txt", "t\n")
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	ask := own.Asks([]string{"feat", "feat"})["origin"]
	if len(ask) != 1 {
		t.Fatalf("asks: %+v", ask)
	}
	own.Failed(ask[0], "could not read from remote repository")
	if o := own.State("feat"); o.State != OwnInSync || o.Fetched || !o.Refuses() || o.FastForwards() ||
		!strings.Contains(o.Refusal(), "could not be fetched (could not read from remote repository)") {
		t.Fatalf("not fetched: %+v %q", o, o.Refusal())
	}
	own, err = ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, f.dir, "fetch", "-q", "origin", ask[0].Refspec())
	own.Seen(ask[0])
	if err := own.Refresh(); err != nil {
		t.Fatal(err)
	}
	if o := own.State("feat"); o.State != OwnBehind || o.Commit != theirs || !o.Fetched || o.Refuses() {
		t.Fatalf("fetched: %+v", o)
	}
}

// Where a push lands cannot be read from a tracking ref when the remote
// pushes somewhere it does not fetch from.
func TestOwnRemoteUnknown(t *testing.T) {
	for name, set := range map[string]func(t *testing.T, f ownFixture){
		"another push URL": func(t *testing.T, f ownFixture) {
			gittest.Git(t, f.dir, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "elsewhere.git"))
		},
		"several push URLs": func(t *testing.T, f ownFixture) {
			gittest.Git(t, f.dir, "remote", "set-url", "--add", "--push", "origin", f.origin)
			gittest.Git(t, f.dir, "remote", "set-url", "--add", "--push", "origin", filepath.Join(t.TempDir(), "second.git"))
		},
		"a mirror": func(t *testing.T, f ownFixture) {
			gittest.Git(t, f.dir, "config", "remote.origin.mirror", "true")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newOwnFixture(t)
			set(t, f)
			own, err := ReadOwn(f.dir, "main")
			if err != nil {
				t.Fatal(err)
			}
			o := own.State("feat")
			if o.State != OwnUnknown || o.Ref != "" || o.Commit != "" || o.Why == "" {
				t.Fatalf("%+v", o)
			}
			if asks := own.Asks([]string{"feat"}); len(asks) != 0 {
				t.Fatalf("asked for %v", asks)
			}
			// A branch nobody pushed is still nothing to check.
			gittest.Git(t, f.dir, "branch", "local-only", "main")
			if o := f.state(t, "local-only"); o.State != OwnNone {
				t.Fatalf("never pushed: %+v", o)
			}
		})
	}
}

func TestSameRepository(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"https://github.com/o/r.git", "git@github.com:o/r.git", true},
		{"https://github.com/o/r", "ssh://git@GitHub.com/o/r.git", true},
		{"https://github.com/o/r", "ssh://git@github.com:22/o/r/", true},
		{"https://github.com/o/r", "https://github.com/o/other", false},
		{"https://github.com/o/r", "https://gitlab.com/o/r", false},
		{"/srv/git/r.git", "/srv/git/r.git", true},
		{"/srv/git/r.git", "/srv/git/r", false},
		{"/srv/git/r.git", "git@host:srv/git/r.git", false},
	} {
		if got := sameRepository(c.a, c.b); got != c.want {
			t.Errorf("sameRepository(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A push remote of the branch's own is where it is compared, and fetched by
// one call to that remote.
func TestOwnRemoteFollowsThePushRemote(t *testing.T) {
	f := newOwnFixture(t)
	fork := filepath.Join(t.TempDir(), "fork.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, fork)
	gittest.Git(t, f.dir, "remote", "add", "fork", fork)
	gittest.Git(t, f.dir, "fetch", "-q", "fork")
	gittest.Git(t, f.dir, "config", "branch.feat.pushRemote", "fork")
	gittest.Git(t, f.dir, "config", "push.default", "current")
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if o := own.State("feat"); o.State != OwnAhead || o.Ref != "fork/feat" {
		t.Fatalf("%+v", o)
	}
	asks := own.Asks([]string{"feat"})
	if got := Remotes(asks); len(got) != 1 || got[0] != "fork" {
		t.Fatalf("remotes %v of %v", got, asks)
	}
}

// Trunk on another remote is no more a branch's own than origin's trunk:
// push.default=upstream makes it the push destination of a branch tracking
// it, and a run must not fast-forward a work branch onto it.
func TestOwnRemoteIsNeverTrunkOnAnotherRemote(t *testing.T) {
	f := newOwnFixture(t)
	fork := filepath.Join(t.TempDir(), "upstream.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, fork)
	gittest.Git(t, f.dir, "remote", "add", "upstream", fork)
	gittest.Git(t, f.dir, "fetch", "-q", "upstream")
	gittest.Git(t, f.dir, "branch", "-q", "--set-upstream-to=upstream/main", "feat")
	gittest.Git(t, f.dir, "config", "push.default", "upstream")
	if got := gittest.Git(t, f.dir, "rev-parse", "--symbolic-full-name", "feat@{push}"); got != "refs/remotes/upstream/main" {
		t.Fatalf("the fixture's push destination is %s", got)
	}
	if o := f.state(t, "feat"); o.State != OwnNone || o.Ref != "" {
		t.Fatalf("%+v", o)
	}
}

// A remote whose fetch refspec does not map every branch is asked for
// nothing: a ref written by name would be one its configuration never maps.
func TestOwnRemoteAsksOnlyAStandardRefspec(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if asks := own.Asks([]string{"feat"}); len(asks) != 0 {
		t.Fatalf("asked for %v", asks)
	}
}

func TestOwnBlocks(t *testing.T) {
	f := newOwnFixture(t)
	behind := OwnRemote{State: OwnBehind, Ref: "origin/feat", Behind: 1}
	if code, _ := OwnBlocks(behind, f.dir, false, nil); code != "" {
		t.Fatalf("clean and idle: %q", code)
	}
	if code, _ := OwnBlocks(behind, f.dir, true, nil); code != OwnBlockDirty {
		t.Fatalf("dirty: %q", code)
	}
	busy := Sessions{{Name: "s", Cwd: f.dir, Status: "busy"}}
	if code, _ := OwnBlocks(behind, f.dir, false, busy); code != OwnBlockSession {
		t.Fatalf("busy session: %q", code)
	}
	// An operation in progress outranks the rest.
	gittest.Git(t, f.dir, "bisect", "start")
	if code, why := OwnBlocks(behind, f.dir, true, busy); code != OwnBlockOperation || !strings.Contains(why, "bisect") {
		t.Fatalf("bisect: %q %q", code, why)
	}
	if code, _ := OwnBlocks(OwnRemote{State: OwnDiverged}, f.dir, false, nil); code != OwnBlockDiverged {
		t.Fatalf("diverged: %q", code)
	}
	for _, s := range []OwnState{OwnNone, OwnGone, OwnUnknown, OwnInSync, OwnAhead, OwnRebased} {
		if code, _ := OwnBlocks(OwnRemote{State: s}, f.dir, true, busy); code != "" {
			t.Errorf("%s blocks with %q", s, code)
		}
	}
}

// A rebase that is put back after the run fast-forwarded the branch goes
// back to the tip the run found, which is what its safety ref pins: not to
// the tip the rebase started from.
func TestRebaseRestoresToTheSafetyRefPinnedBeforeAFastForward(t *testing.T) {
	f := newOwnFixture(t)
	found := gittest.Git(t, f.dir, "rev-parse", "feat")
	// The remote's commit and trunk both write c.txt: the rebase stops on
	// the remote's commit, and nothing is declared to resolve it.
	theirs := f.pushFromOther(t, "feat", "c.txt", "theirs\n")
	f.pushFromOther(t, "main", "c.txt", "trunk\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	safety, err := WriteSafety(f.dir, "feat", found, 3)
	if err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, f.dir, "merge", "-q", "--ff-only", theirs)
	req := Request{Path: f.dir, Branch: "feat", Trunk: "origin/main", Onto: "origin/main", Epoch: 3, Stacked: true, Safety: safety}
	res, err := Rebase(f.dir, &Config{}, req, nil)
	if err != nil || !res.Restored {
		t.Fatalf("err %v, result %+v", err, res)
	}
	if res.OldTip != theirs || res.Safety.Tip != found {
		t.Fatalf("old tip %s, safety %+v", res.OldTip, res.Safety)
	}
	if head := gittest.Git(t, f.dir, "rev-parse", "HEAD"); head != found {
		t.Fatalf("restored to %s, want the tip the run found %s", head, found)
	}
	if ref := gittest.Git(t, f.dir, "symbolic-ref", "HEAD"); ref != "refs/heads/feat" {
		t.Fatalf("HEAD is %s", ref)
	}
	if st := gittest.Git(t, f.dir, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Fatalf("left changes:\n%s", st)
	}
}

// Preflight for a branch behind its own remote: with nothing to rebase at
// the remote's commit the run still goes, to fast-forward it; where it
// cannot be fast-forwarded it is refused whatever its class. A diverged
// branch a run would not rebase anyway is skipped as before.
func TestPreflightForABranchBehindItsRemote(t *testing.T) {
	behind := OwnRemote{State: OwnBehind, Ref: "origin/x", Behind: 2}
	blocked := behind
	blocked.Blocks, blocked.Why = OwnBlockOperation, "a bisect is in progress"
	for _, c := range []struct {
		name string
		a    Assessment
		want Verdict
	}{
		{"on trunk there", Assessment{Class: Current, Own: behind}, Proceed},
		{"nothing of its own there", Assessment{Class: Stale, Own: behind}, Proceed},
		{"blocked, on trunk", Assessment{Class: Current, Own: blocked}, RefuseRun},
		{"blocked, to rebase", Assessment{Class: Clean, Own: blocked}, RefuseRun},
		{"diverged, on trunk", Assessment{Class: Current, Own: OwnRemote{State: OwnDiverged, Blocks: OwnBlockDiverged}}, SkipRun},
		{"in sync, on trunk", Assessment{Class: Current, Own: OwnRemote{State: OwnInSync}}, SkipRun},
	} {
		if got, why := Preflight(c.a); got != c.want {
			t.Errorf("%s: verdict %d (%q), want %d", c.name, got, why, c.want)
		}
	}
	if a := (Assessment{Class: Current, Own: behind}); !a.OnlyFastForward() {
		t.Error("behind and on trunk at the remote's commit is a fast-forward and nothing else")
	}
	if a := (Assessment{Class: Clean, Own: behind}); a.OnlyFastForward() {
		t.Error("a branch with something to rebase is not only fast-forwarded")
	}
}

// A partial clone has git print its filter after the URL in git remote -v:
// that must not make every branch of it unreadable.
func TestOwnRemoteReadsAPartialClone(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "config", "remote.origin.promisor", "true")
	gittest.Git(t, f.dir, "config", "remote.origin.partialclonefilter", "blob:none")
	if out := gittest.Git(t, f.dir, "remote", "-v"); !strings.Contains(out, "[blob:none]") {
		t.Skipf("this git prints no filter in remote -v:\n%s", out)
	}
	if o := f.state(t, "feat"); o.State != OwnInSync || o.Ref != "origin/feat" {
		t.Fatalf("%+v", o)
	}
}

// A merge on the remote side has no patch to match: a branch that was
// rebased, whose remote then gained a merge commit, is diverged.
func TestOwnRemoteAMergeOnTheRemoteIsDiverged(t *testing.T) {
	f := newOwnFixture(t)
	f.pushFromOther(t, "main", "m.txt", "m\n")
	gittest.Git(t, f.other, "checkout", "-q", "-B", "feat", "origin/feat")
	gittest.Git(t, f.other, "merge", "-q", "--no-edit", "origin/main")
	gittest.Git(t, f.other, "push", "-q", "origin", "feat")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	gittest.Git(t, f.dir, "reflog", "expire", "--expire=now", "--all")
	if o := f.state(t, "feat"); o.State != OwnDiverged {
		t.Fatalf("%+v", o)
	}
}

// push.default matching pushes a branch to its own name, and a push refspec
// that renames sends it somewhere else: git resolves both, and the ref it
// names is the one compared.
func TestOwnRemoteFollowsMatchingAndARenamingPushRefspec(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "config", "push.default", "matching")
	if o := f.state(t, "feat"); o.State != OwnInSync || o.Ref != "origin/feat" {
		t.Fatalf("matching: %+v", o)
	}
	gittest.Git(t, f.dir, "config", "--unset", "push.default")
	gittest.Git(t, f.dir, "config", "remote.origin.push", "refs/heads/feat:refs/heads/review/feat")
	gittest.Git(t, f.dir, "push", "-q", "origin")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	own, err := ReadOwn(f.dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if o := own.State("feat"); o.State != OwnAhead || o.Ref != "origin/review/feat" {
		t.Fatalf("renamed: %+v", o)
	}
	if got := own.Asks([]string{"feat"})["origin"]; len(got) != 1 || got[0].Refspec() != "+refs/heads/review/feat:refs/remotes/origin/review/feat" {
		t.Fatalf("asks %+v", got)
	}
}

// push.default upstream makes a stack child that tracks its parent push to
// a local branch: no destination on a remote, so the branch's own name on
// origin is what is compared.
func TestOwnRemoteOfABranchThatTracksALocalBranch(t *testing.T) {
	f := newOwnFixture(t)
	gittest.Git(t, f.dir, "checkout", "-q", "-b", "child")
	f.commit(t, f.dir, "c.txt", "c\n", "child 1")
	gittest.Git(t, f.dir, "push", "-q", "origin", "child")
	gittest.Git(t, f.dir, "branch", "-q", "--set-upstream-to=feat", "child")
	gittest.Git(t, f.dir, "config", "push.default", "upstream")
	if got := gittest.Git(t, f.dir, "rev-parse", "--symbolic-full-name", "child@{push}"); got != "refs/heads/feat" {
		t.Fatalf("the fixture's push destination is %s", got)
	}
	f.commit(t, f.dir, "c2.txt", "c2\n", "child 2")
	if o := f.state(t, "child"); o.State != OwnAhead || o.Ref != "origin/child" || o.Ahead != 1 {
		t.Fatalf("%+v", o)
	}
}

// A safety ref pinned for a branch that was since deleted says nothing about
// a new branch that took its name: the remote still holding the old one's
// commit is not something the new one ever had.
func TestOwnRemoteOfARecreatedBranchIgnoresTheOldOnesRefs(t *testing.T) {
	f := newOwnFixture(t)
	pushed := gittest.Git(t, f.dir, "rev-parse", "feat")
	if _, err := WriteSafety(f.dir, "feat", pushed, 7); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, f.dir, "checkout", "-q", "main")
	gittest.Git(t, f.dir, "branch", "-q", "-D", "feat")
	gittest.Git(t, f.dir, "checkout", "-q", "-b", "feat")
	f.commit(t, f.dir, "new.txt", "new\n", "another feat")
	if o := f.state(t, "feat"); o.State != OwnDiverged {
		t.Fatalf("the old branch's safety ref made its tip a former tip of the new one: %+v", o)
	}
}

// Disown drops the reflog entries a run's fast-forward wrote and nothing
// else: an older entry at the same commit is the person's own, and what is
// left still reads as a chain.
func TestDisownDropsOnlyTheRunsOwnReflogEntries(t *testing.T) {
	f := newOwnFixture(t)
	local := gittest.Git(t, f.dir, "rev-parse", "feat")
	theirs := f.pushFromOther(t, "feat", "t.txt", "t\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	at := func(seconds string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = f.dir
		cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+seconds+" +0000", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// The person was at the remote's commit once and went back; much later a
	// run fast-forwards there, rebases nowhere and is undone.
	at("1000000000", "merge", "-q", "--ff-only", theirs)
	at("1000002000", "reset", "-q", "--hard", local)
	const epoch = int64(1000005000) * int64(time.Second)
	if err := WriteRun(f.dir, epoch, []Pin{{Branch: "feat", Safety: local, Forward: theirs}}); err != nil {
		t.Fatal(err)
	}
	at("1000006000", "merge", "-q", "--ff-only", theirs)
	if err := WriteResult(f.dir, "feat", theirs, epoch); err != nil {
		t.Fatal(err)
	}
	at("1000007000", "reset", "-q", "--hard", local)
	if err := Disown(f.dir, "feat", epoch); err != nil {
		t.Fatal(err)
	}
	log := gittest.Git(t, f.dir, "reflog", "show", "--format=%H %gd", "--date=unix", "refs/heads/feat")
	if strings.Contains(log, theirs+" feat@{1000006000}") || !strings.Contains(log, theirs+" feat@{1000000000}") {
		t.Fatalf("the reflog after disowning:\n%s", log)
	}
	// Each entry's old value is the entry before it.
	raw, err := os.ReadFile(filepath.Join(f.dir, ".git", "logs", "refs", "heads", "feat"))
	if err != nil {
		t.Fatal(err)
	}
	prev := ""
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if prev != "" && fields[0] != prev {
			t.Fatalf("the reflog no longer reads as a chain at %q:\n%s", line, raw)
		}
		prev = fields[1]
	}
	for _, ref := range []string{forwardRef("feat", epoch), resultRef("feat", epoch)} {
		if _, ok, _ := refTip(f.dir, ref); ok {
			t.Errorf("%s is still there", ref)
		}
	}
	if _, ok, _ := refTip(f.dir, SafetyRef("feat", epoch)); !ok {
		t.Error("the safety ref went too")
	}
}

// A fast-forward a run pinned and never made is not a place the branch was.
func TestOwnRemoteAFastForwardThatNeverHappenedIsNoFormerTip(t *testing.T) {
	f := newOwnFixture(t)
	local := gittest.Git(t, f.dir, "rev-parse", "feat")
	theirs := f.pushFromOther(t, "feat", "t.txt", "t\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if err := WriteRun(f.dir, 3, []Pin{{Branch: "feat", Safety: local, Forward: theirs}}); err != nil {
		t.Fatal(err)
	}
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	if o := f.state(t, "feat"); o.State != OwnDiverged {
		t.Fatalf("%+v", o)
	}
}

// The fast-forward ref is what says so once the reflog is gone: a run that
// fast-forwarded and finished its rebase left the remote's commit a former
// tip for good.
func TestOwnRemoteAFinishedFastForwardIsAFormerTipWithoutAReflog(t *testing.T) {
	f := newOwnFixture(t)
	local := gittest.Git(t, f.dir, "rev-parse", "feat")
	theirs := f.pushFromOther(t, "feat", "f.txt", "theirs\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if err := WriteRun(f.dir, 3, []Pin{{Branch: "feat", Safety: local, Forward: theirs}}); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, f.dir, "merge", "-q", "--ff-only", theirs)
	gittest.Git(t, f.dir, "commit", "-q", "--amend", "-m", "rewritten as a rebase would")
	if err := WriteResult(f.dir, "feat", gittest.Git(t, f.dir, "rev-parse", "HEAD"), 3); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, f.dir, "reflog", "expire", "--expire=now", "--all")
	if o := f.state(t, "feat"); o.State != OwnRebased {
		t.Fatalf("%+v", o)
	}
}

// Remotes that cannot be read are not "nothing to check": every branch is
// unknown, with why.
func TestReadOwnOrUnknownSaysWhy(t *testing.T) {
	own := ReadOwnOrUnknown(t.TempDir(), "main")
	if o := own.State("feat"); o.State != OwnUnknown || !strings.HasPrefix(o.Why, "the remotes cannot be read: ") {
		t.Fatalf("%+v", o)
	}
	if asks := own.Asks([]string{"feat"}); len(asks) != 0 {
		t.Fatalf("asks %v", asks)
	}
	if err := own.Refresh(); err != nil {
		t.Fatal(err)
	}
}

// A fast-forward a run made and never got past, because it was interrupted
// before the rebase: the branch was at the remote's commit only on the way
// through a run that did not finish. Reset back by hand, with a commit on
// top, it has diverged, whatever its reflog still says.
func TestOwnRemoteAFastForwardOfAnUnfinishedRunIsNoFormerTip(t *testing.T) {
	f := newOwnFixture(t)
	local := gittest.Git(t, f.dir, "rev-parse", "feat")
	theirs := f.pushFromOther(t, "feat", "t.txt", "t\n")
	gittest.Git(t, f.dir, "fetch", "-q", "origin")
	if err := WriteRun(f.dir, 3, []Pin{{Branch: "feat", Safety: local, Forward: theirs}}); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, f.dir, "merge", "-q", "--ff-only", theirs)
	gittest.Git(t, f.dir, "reset", "-q", "--hard", SafetyRef("feat", 3))
	f.commit(t, f.dir, "g.txt", "g\n", "feat 2")
	if o := f.state(t, "feat"); o.State != OwnDiverged {
		t.Fatalf("%+v", o)
	}
	// Once the run has a result, it finished, and the commit is one the
	// branch had.
	if err := WriteResult(f.dir, "feat", theirs, 3); err != nil {
		t.Fatal(err)
	}
	if o := f.state(t, "feat"); o.State != OwnRebased {
		t.Fatalf("finished: %+v", o)
	}
}
