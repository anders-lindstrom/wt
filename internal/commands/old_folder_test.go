package commands

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/purge/purgetest"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// A folder a wt from before --move-to made is put back by this one: its
// record, its lock and its pins are read under the names they were written
// with.
func TestRestorePutsBackAFolderMadeTheOldWay(t *testing.T) {
	t.Parallel()
	ctx, path, branch, tip, dir := provisiontest.OldFolder(t, "fix/old-way")
	res := refstest.Restore(t, ctx, dir)
	if res.Outcome != quarantine.OutcomeRestored {
		t.Fatalf("outcome %s", res.Outcome)
	}
	testkit.AssertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	if got := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", branch)); got != tip {
		t.Errorf("the branch is at %s, want %s", got, tip)
	}
	if pins := testkit.GitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-quarantine/"); strings.TrimSpace(pins) != "" {
		t.Errorf("the pins must be gone: %s", pins)
	}
}

// And it is purged by this one, its three pins with it.
func TestPurgeDeletesAFolderMadeTheOldWay(t *testing.T) {
	t.Parallel()
	ctx, _, _, _, dir := provisiontest.OldFolder(t, "fix/old-way")
	res := purgetest.Purge(t, dir)
	if res.Outcome != quarantine.OutcomePurged {
		t.Fatalf("outcome %s", res.Outcome)
	}
	testkit.AssertPurged(t, ctx, dir)
}
