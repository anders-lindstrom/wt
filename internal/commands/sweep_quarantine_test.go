package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Each worktree a sweep removes goes into its own folder in the quarantine,
// with its own recovery.json, and wt restore puts any one of them back.
func TestSweepQuarantinesEachWorktreeInItsOwnFolder(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	one := mergedWorktree(t, ctx, "fix/one")
	two := mergedWorktree(t, ctx, "feat/one")
	dir := trashFor(t, ctx)
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	r, human, err := sweepJSON(t, ctx, opts)
	if err != nil || r.Outcome != OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	if r.Quarantine == nil || *r.Quarantine != dir {
		t.Errorf("quarantine %v", r.Quarantine)
	}
	subs := map[string]bool{}
	for _, it := range r.Items {
		if it.Category != SweepRemove {
			continue
		}
		q := it.Quarantine
		if q == nil || !q.CheckoutMoved || !q.AdminMoved || filepath.Dir(q.Dir) != dir || it.Result != SweepRemoved {
			t.Fatalf("item %+v quarantine %+v", it, q)
		}
		subs[q.Dir] = true
		if rec := loadRecord(t, q.Dir); rec.Command != "sweep" || !strings.Contains(rec.Checkout.Path, "one") {
			t.Errorf("record %+v", rec)
		}
	}
	if len(subs) != 2 {
		t.Fatalf("want a folder each, got %v", subs)
	}
	if exists(one) || exists(two) {
		t.Error("both worktrees should be gone from their places")
	}
	for sub := range subs {
		restore(t, ctx, sub)
	}
	if !exists(one) || !exists(two) {
		t.Error("wt restore puts each back")
	}
}

// The folder that exists, or a worktree on another volume, refuses the
// sweep before anything is deleted.
func TestSweepQuarantineRefusesBeforeAnyChange(t *testing.T) {
	for _, why := range []string{"exists", "volume"} {
		t.Run(why, func(t *testing.T) {
			ctx, main, _ := sweepRepo(t)
			branchWithWork(t, main, "done-work", 1)
			landOnMain(t, main, "done-work")
			path := mergedWorktree(t, ctx, "fix/stay")
			dir := trashFor(t, ctx)
			switch why {
			case "exists":
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			case "volume":
				orig := quarantine.DeviceOf
				t.Cleanup(func() { quarantine.DeviceOf = orig })
				quarantine.DeviceOf = func(p string) (uint64, error) {
					if p == filepath.Dir(dir) {
						return 4242, nil
					}
					return orig(p)
				}
			}
			opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
			r, human, err := sweepJSON(t, ctx, opts)
			if err == nil || r.Outcome != OutcomeRefused || r.Error == nil {
				t.Fatalf("want a refusal: %v %+v\n%s", err, r, human)
			}
			if !exists(path) || !ctx.Repo.BranchExists("done-work") {
				t.Error("nothing may be swept")
			}
		})
	}
}

// A quarantine folder that cannot be used refuses the sweep even when only
// branches would go.
func TestSweepQuarantineRefusesABranchOnlySweep(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	dir := trashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	r, human, err := sweepJSON(t, ctx, opts)
	if err == nil || r.Outcome != OutcomeRefused || !ctx.Repo.BranchExists("done-work") {
		t.Fatalf("want a refusal: %v %+v\n%s", err, r, human)
	}
}

// The plan a tool reads says where the worktrees go, refuses a folder that
// cannot be used, and its token holds the run to the same folder: a token
// read for a quarantine does not let a run delete instead.
func TestSweepPlanJSONCarriesTheQuarantine(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	mergedWorktree(t, ctx, "fix/planned")
	dir := trashFor(t, ctx)
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	p, err := sweepPlanJSON(t, ctx, opts)
	if err != nil || p.Token == nil || p.Quarantine == nil || *p.Quarantine != dir {
		t.Fatalf("%v %+v", err, p)
	}
	plain := opts
	plain.Quarantine = ""
	q, _ := sweepPlanJSON(t, ctx, plain)
	if q.Token == nil || *q.Token == *p.Token {
		t.Error("the token must differ with and without a quarantine")
	}
	plain.Expect = *p.Token
	if r, _, err := sweepJSON(t, ctx, plain); err == nil || r.Outcome != OutcomeRefused {
		t.Errorf("a run without the quarantine must refuse the token: %v %+v", err, r)
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := sweepPlanJSON(t, ctx, opts); err == nil || p.Error == nil || p.Token != nil {
		t.Errorf("a folder that is there already refuses the plan: %v %+v", err, p)
	}
}

// A worktree that moved and whose run then failed is reported as what it
// is, not as kept.
func TestSweepSaysAPartialMoveIsPartial(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	mergedWorktree(t, ctx, "fix/half")
	dir := trashFor(t, ctx)
	t.Cleanup(func() { quarantine.AfterRename = nil })
	quarantine.AfterRename = func(to string) error {
		if filepath.Base(to) == "admin" {
			return errors.New("fsync: input/output error")
		}
		return nil
	}
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	r, human, _ := sweepJSON(t, ctx, opts)
	if strings.Contains(human, "- kept half") || !strings.Contains(human, "partly done") {
		t.Errorf("the output must say it is partly done, not kept:\n%s", human)
	}
	if r.Outcome != OutcomePartial {
		t.Errorf("outcome %s", r.Outcome)
	}
}

func TestSweepQuarantineRefusesAFolderInsideTheRepository(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/stays")
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{},
		Quarantine: filepath.Join(main, "trash-q")}
	if r, _, err := sweepJSON(t, ctx, opts); err == nil || r.Outcome != OutcomeRefused || !exists(path) {
		t.Errorf("want a refusal: %v %+v", err, r)
	}
}

// A quarantine the user deleted — the Trash emptied — no longer needs its
// commits pinned: a sweep drops its pins. One whose folder's parent is gone
// too may be on a volume that is not mounted, and keeps them.
func TestSweepDropsThePinsOfADeletedQuarantine(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	gone := mergedWorktree(t, ctx, "fix/emptied")
	away := mergedWorktree(t, ctx, "fix/unmounted")
	trash := filepath.Join(ctx.Repo.Parent, "trash")
	volume := filepath.Join(ctx.Repo.Parent, "volume", "trash")
	for _, d := range []string{trash, volume} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	for path, dir := range map[string]string{gone: filepath.Join(trash, "q"), away: filepath.Join(volume, "q")} {
		if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: dir}, &buf); err != nil {
			t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
		}
	}
	if err := os.RemoveAll(filepath.Join(trash, "q")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(ctx.Repo.Parent, "volume")); err != nil {
		t.Fatal(err)
	}
	branchWithWork(t, main, "done-work", 1)
	landOnMain(t, main, "done-work")
	opts := SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}}
	_, human, err := sweepJSON(t, ctx, opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	pins := gitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/wt-quarantine/")
	if strings.Contains(pins, "emptied") || !strings.Contains(pins, "unmounted") {
		t.Errorf("pins left:\n%s", pins)
	}
	if !strings.Contains(human, "dropped the pins of "+filepath.Join(trash, "q")) {
		t.Errorf("it must say so:\n%s", human)
	}
}
