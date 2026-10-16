package retire_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The folder that exists, or a worktree on another volume, refuses the
// sweep before anything is deleted.
func TestSweepQuarantineRefusesBeforeAnyChange(t *testing.T) {
	for _, why := range []string{"exists", "volume"} {
		t.Run(why, func(t *testing.T) {
			ctx, main, _ := testkit.SweepRepo(t)
			testkit.BranchWithWork(t, main, "done-work", 1)
			testkit.LandOnMain(t, main, "done-work")
			path := provisiontest.MergedWorktree(t, ctx, "fix/stay")
			dir := testkit.TrashFor(t, ctx)
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
			opts := retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
			r, human, err := retiretest.SweepJSON(t, ctx, opts)
			if err == nil || r.Outcome != kit.OutcomeRefused || r.Error == nil {
				t.Fatalf("want a refusal: %v %+v\n%s", err, r, human)
			}
			if !testkit.Exists(path) || !ctx.Repo.BranchExists("done-work") {
				t.Error("nothing may be swept")
			}
		})
	}
}

// A quarantine folder that cannot be used refuses the sweep even when only
// branches would go.
func TestSweepQuarantineRefusesABranchOnlySweep(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	dir := testkit.TrashFor(t, ctx)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	r, human, err := retiretest.SweepJSON(t, ctx, opts)
	if err == nil || r.Outcome != kit.OutcomeRefused || !ctx.Repo.BranchExists("done-work") {
		t.Fatalf("want a refusal: %v %+v\n%s", err, r, human)
	}
}

// The plan a tool reads says where the worktrees go, refuses a folder that
// cannot be used, and its token holds the run to the same folder: a token
// read for a quarantine does not let a run delete instead.
func TestSweepPlanJSONCarriesTheQuarantine(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	provisiontest.MergedWorktree(t, ctx, "fix/planned")
	dir := testkit.TrashFor(t, ctx)
	opts := retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	p, err := retiretest.SweepPlanJSON(t, ctx, opts)
	if err != nil || p.Token == nil || p.Quarantine == nil || *p.Quarantine != dir {
		t.Fatalf("%v %+v", err, p)
	}
	plain := opts
	plain.Quarantine = ""
	q, _ := retiretest.SweepPlanJSON(t, ctx, plain)
	if q.Token == nil || *q.Token == *p.Token {
		t.Error("the token must differ with and without a quarantine")
	}
	plain.Expect = *p.Token
	if r, _, err := retiretest.SweepJSON(t, ctx, plain); err == nil || r.Outcome != kit.OutcomeRefused {
		t.Errorf("a run without the quarantine must refuse the token: %v %+v", err, r)
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := retiretest.SweepPlanJSON(t, ctx, opts); err == nil || p.Error == nil || p.Token != nil {
		t.Errorf("a folder that is there already refuses the plan: %v %+v", err, p)
	}
}

// A worktree that moved and whose run then failed is reported as what it
// is, not as kept.
func TestSweepSaysAPartialMoveIsPartial(t *testing.T) {
	ctx, _, _ := testkit.SweepRepo(t)
	provisiontest.MergedWorktree(t, ctx, "fix/half")
	dir := testkit.TrashFor(t, ctx)
	t.Cleanup(func() { quarantine.AfterRename = nil })
	quarantine.AfterRename = func(to string) error {
		if filepath.Base(to) == "admin" {
			return errors.New("fsync: input/output error")
		}
		return nil
	}
	opts := retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}, Quarantine: dir}
	r, human, _ := retiretest.SweepJSON(t, ctx, opts)
	if strings.Contains(human, "- kept half") || !strings.Contains(human, "partly done") {
		t.Errorf("the output must say it is partly done, not kept:\n%s", human)
	}
	if r.Outcome != kit.OutcomePartial {
		t.Errorf("outcome %s", r.Outcome)
	}
}

func TestSweepQuarantineRefusesAFolderInsideTheRepository(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	path := provisiontest.MergedWorktree(t, ctx, "fix/stays")
	opts := retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{},
		Quarantine: filepath.Join(main, "trash-q")}
	if r, _, err := retiretest.SweepJSON(t, ctx, opts); err == nil || r.Outcome != kit.OutcomeRefused || !testkit.Exists(path) {
		t.Errorf("want a refusal: %v %+v", err, r)
	}
}

// A quarantine the user deleted — the Trash emptied — no longer needs its
// commits pinned: a sweep drops its pins. One whose folder's parent is gone
// too may be on a volume that is not mounted, and keeps them.
func TestSweepDropsThePinsOfADeletedQuarantine(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	gone := provisiontest.MergedWorktree(t, ctx, "fix/emptied")
	away := provisiontest.MergedWorktree(t, ctx, "fix/unmounted")
	trash := filepath.Join(ctx.Repo.Parent, "trash")
	volume := filepath.Join(ctx.Repo.Parent, "volume", "trash")
	for _, d := range []string{trash, volume} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	for path, dir := range map[string]string{gone: filepath.Join(trash, "q"), away: filepath.Join(volume, "q")} {
		if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: dir}, &buf); err != nil {
			t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
		}
	}
	if err := os.RemoveAll(filepath.Join(trash, "q")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(ctx.Repo.Parent, "volume")); err != nil {
		t.Fatal(err)
	}
	testkit.BranchWithWork(t, main, "done-work", 1)
	testkit.LandOnMain(t, main, "done-work")
	opts := retire.SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}}
	_, human, err := retiretest.SweepJSON(t, ctx, opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	pins := testkit.GitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/wt-quarantine/")
	if strings.Contains(pins, "emptied") || !strings.Contains(pins, "unmounted") {
		t.Errorf("pins left:\n%s", pins)
	}
	if !strings.Contains(human, "dropped the pins of "+filepath.Join(trash, "q")) {
		t.Errorf("it must say so:\n%s", human)
	}
}
