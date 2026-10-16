package synccmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// An interrupt between the fast-forward and the rebase has no rebase to
// abort: the way back is wt sync undo, which also takes away the word that
// the branch was at its remote's commit.
func TestOnInterruptAfterAFastForwardPointsAtUndo(t *testing.T) {
	var out bytes.Buffer
	kit.OnInterrupt(&out, nil, &kit.RebaseInFlight{Work: "bump", Path: "/w/bump", Safety: "refs/wt-sync/feat_wt/bump/99", Forwarding: true})
	s := out.String()
	if !strings.Contains(s, "interrupted while fast-forwarding bump") || !strings.Contains(s, "wt sync undo bump") {
		t.Fatalf("out %q", s)
	}
	if strings.Contains(s, "rebase --abort") || strings.Contains(s, "reset --hard") {
		t.Fatalf("the interrupt advises a way back that leaves the remote's commit a former tip: %q", s)
	}
}
