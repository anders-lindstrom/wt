package synccmdtest

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// StackYAML is what StackLine declares on trunk: the owned-line rule that
// resolves s3's bump of v.txt against trunk's.
const StackYAML = "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"

// StackLine is gittest's StackLine in a repository wt opens, with yaml
// declared on trunk (StackYAML when empty).
func StackLine(t *testing.T, yaml string) (*kit.Context, *gittest.StackLine) {
	t.Helper()
	if yaml == "" {
		yaml = StackYAML
	}
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	testkit.GitIn(t, main, "config", "core.hooksPath", filepath.Join(main, ".git", "hooks"))
	wtDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := gittest.NewStackLine(t, main, wtDir, yaml)
	ctx, err := kit.Open(main, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s
}

// StackRun runs wt sync rebase <work> --stack --json with opts and returns
// its checked result and what it printed for a person.
func StackRun(t *testing.T, ctx *kit.Context, work string, opts synccmd.RunOptions) (syncrun.RunResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = syncrun.NewSyncRunJournal(&out, "sync rebase")
	if opts.Stack == nil {
		opts.Stack = &synccmd.StackOptions{}
	}
	err := synccmd.SyncRebase(ctx, []string{work}, opts, &human)
	return DecodeSyncRun(t, out.Bytes()), human.String(), err
}

// Participant is the run's row for branch.
func Participant(t *testing.T, r syncrun.RunResult, branch string) *syncrun.SyncParticipant {
	t.Helper()
	for _, p := range r.Worktrees {
		if p.Branch == branch {
			return p
		}
	}
	t.Fatalf("no row for %s in %+v", branch, r.Worktrees)
	return nil
}
