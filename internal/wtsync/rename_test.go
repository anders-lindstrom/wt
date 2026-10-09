package wtsync

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/repo"
)

func recordedFor(t *testing.T, f ownFixture, branch string) string {
	t.Helper()
	to, ok, err := (&repo.Repo{MainRoot: f.dir}).PushTo(branch)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return to.String()
}

func (f ownFixture) rename(t *testing.T, from, to string) string {
	t.Helper()
	kept, err := RenameKeepingPush(&repo.Repo{MainRoot: f.dir}, "main", from, to)
	if err != nil {
		t.Fatalf("rename %s to %s: %v", from, to, err)
	}
	if kept == nil {
		return ""
	}
	return kept.String()
}

func TestRenameKeepingPushRecordsWhereTheOldNamePushed(t *testing.T) {
	f := newOwnFixture(t)
	if kept := f.rename(t, "feat", "feat_wt/new"); kept != "origin/feat" || recordedFor(t, f, "feat_wt/new") != "origin/feat" {
		t.Fatalf("kept %q, recorded %q", kept, recordedFor(t, f, "feat_wt/new"))
	}
	if up := gittest.Git(t, f.dir, "rev-parse", "--abbrev-ref", "feat_wt/new@{upstream}"); up != "origin/feat" {
		t.Fatalf("upstream %q", up)
	}
	// A second rename carries the record and says where it still pushes.
	if kept := f.rename(t, "feat_wt/new", "feat_wt/newer"); kept != "origin/feat" || recordedFor(t, f, "feat_wt/newer") != "origin/feat" {
		t.Fatalf("kept %q, recorded %q", kept, recordedFor(t, f, "feat_wt/newer"))
	}
	// Back under the name it records: nothing left for a record to say.
	if kept := f.rename(t, "feat_wt/newer", "feat"); kept != "" || recordedFor(t, f, "feat") != "" {
		t.Fatalf("kept %q, recorded %q", kept, recordedFor(t, f, "feat"))
	}
	// Pushed without -u: no upstream, and the remote has it all the same.
	gittest.Git(t, f.dir, "branch", "-q", "--unset-upstream", "feat")
	if kept := f.rename(t, "feat", "feat_wt/again"); kept != "origin/feat" {
		t.Fatalf("pushed with no upstream: kept %q", kept)
	}
}

// A fork: the branch tracks the shared repository's branch of its name and
// pushes to the person's own. What is recorded is where it pushed.
func TestRenameKeepingPushRecordsThePushRemoteNotTheUpstream(t *testing.T) {
	for name, config := range map[string]string{"remote.pushDefault": "remote.pushDefault", "branch.<name>.pushRemote": "branch.shared-fix.pushRemote"} {
		t.Run(name, func(t *testing.T) {
			f := newOwnFixture(t)
			shared := filepath.Join(t.TempDir(), "shared.git")
			gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, shared)
			gittest.Git(t, f.dir, "remote", "add", "upstream", shared)
			gittest.Git(t, f.dir, "push", "-q", "upstream", "main:refs/heads/shared-fix")
			gittest.Git(t, f.dir, "fetch", "-q", "upstream")
			gittest.Git(t, f.dir, "checkout", "-q", "-b", "shared-fix", "--track", "upstream/shared-fix")
			gittest.Git(t, f.dir, "config", config, "origin")
			f.commit(t, f.dir, "k.txt", "k\n", "K")
			gittest.Git(t, f.dir, "push", "-q", "origin", "shared-fix")
			theirs := on(t, shared, "shared-fix")
			if p := f.own(t).Push("shared-fix"); p.Ref() != "origin/shared-fix" {
				t.Fatalf("before the rename it pushes to %s", p.Ref())
			}
			if kept := f.rename(t, "shared-fix", "feat_wt/shared-fix"); kept != "origin/shared-fix" {
				t.Fatalf("kept %q: the upstream is not where it pushed", kept)
			}
			p := f.own(t).Push("feat_wt/shared-fix")
			if p.Ref() != "origin/shared-fix" {
				t.Fatalf("after the rename it pushes to %s", p.Ref())
			}
			f.commit(t, f.dir, "k2.txt", "k2\n", "K2")
			gittest.Git(t, f.dir, f.own(t).Push("feat_wt/shared-fix").Args()...)
			if on(t, shared, "shared-fix") != theirs {
				t.Fatal("the push wrote the shared repository's branch")
			}
			if on(t, f.origin, "shared-fix") != gittest.Git(t, f.dir, "rev-parse", "feat_wt/shared-fix") {
				t.Fatal("the push did not reach the fork")
			}
		})
	}
}

// Only where the old name pushed, and only when the remote has that branch
// or the upstream names it.
func TestRenameKeepingPushRecordsNothingItCannotShow(t *testing.T) {
	f := newOwnFixture(t)
	f.theirs(t, "theirs")
	gittest.Git(t, f.dir, "branch", "plain", "main")
	gittest.Git(t, f.dir, "branch", "--track", "on-trunk", "origin/main")
	gittest.Git(t, f.dir, "branch", "--track", "on-theirs", "origin/theirs")
	gittest.Git(t, f.dir, "branch", "--track", "on-local", "feat")
	gittest.Git(t, f.dir, "branch", "--track", "on-parent", "origin/feat")
	for _, b := range []string{"plain", "on-trunk", "on-theirs", "on-local", "on-parent"} {
		if kept := f.rename(t, b, "feat_wt/"+b); kept != "" || recordedFor(t, f, "feat_wt/"+b) != "" {
			t.Errorf("%s: kept %q, recorded %q", b, kept, recordedFor(t, f, "feat_wt/"+b))
		}
	}
	// A name already taken fails as git branch -m does, with nothing written.
	gittest.Git(t, f.dir, "branch", "taken", "main")
	if _, err := RenameKeepingPush(&repo.Repo{MainRoot: f.dir}, "main", "feat", "taken"); err == nil {
		t.Fatal("renamed onto a branch that exists")
	}
	if recordedFor(t, f, "feat") != "" || recordedFor(t, f, "taken") != "" {
		t.Fatal("a failed rename recorded a destination")
	}
	// A record somebody wrote by hand for another remote stays through a
	// rename back to the name it holds: it is not where the branch would
	// push with nothing recorded.
	fork := filepath.Join(t.TempDir(), "fork.git")
	gittest.Git(t, f.dir, "clone", "-q", "--bare", f.origin, fork)
	gittest.Git(t, f.dir, "remote", "add", "fork", fork)
	gittest.Git(t, f.dir, "branch", "-m", "feat", "elsewhere")
	gittest.Git(t, f.dir, "config", "branch.elsewhere.wtPushTo", "fork feat")
	if kept := f.rename(t, "elsewhere", "feat"); kept != "" || recordedFor(t, f, "feat") != "fork/feat" {
		t.Fatalf("kept %q, recorded %q", kept, recordedFor(t, f, "feat"))
	}
}
