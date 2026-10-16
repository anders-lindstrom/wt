package synccmd_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// The doctor's plan row says where each handover stands, as the overview's
// row does: waiting at its stop, or aborted by hand.
func TestSyncDoctorSaysWhereAHandoverStands(t *testing.T) {
	t.Parallel()
	ctx, bump, _, _ := synccmdtest.HandedOver(t)
	testkit.GitOut(t, ctx.Repo.MainRoot, "config", "core.hooksPath", filepath.Join(ctx.Repo.MainRoot, ".git", "hooks"))
	planRow := func() string {
		t.Helper()
		var out bytes.Buffer
		if err := synccmd.SyncDoctor(ctx, synccmd.DoctorOptions{}, &out); err != nil {
			t.Fatalf("err %v\n%s", err, out.String())
		}
		for _, row := range synccmd.DoctorRows(t, out.String()) {
			if strings.HasPrefix(row, "plan ") {
				return row
			}
		}
		t.Fatalf("no plan row:\n%s", out.String())
		return ""
	}
	if row, want := planRow(), "bump: "+wtsync.WayOut(wtsync.Way{Work: "bump", Path: bump, Plan: true, Rebasing: true}); !strings.Contains(row, want) {
		t.Fatalf("waiting: row %q, want %q", row, want)
	}
	testkit.GitOut(t, bump, "rebase", "--abort")
	if row, want := planRow(), "bump: "+wtsync.WayOut(wtsync.Way{Work: "bump", Path: bump, Plan: true, Aborted: true}); !strings.Contains(row, want) {
		t.Fatalf("aborted: row %q, want %q", row, want)
	}
}
