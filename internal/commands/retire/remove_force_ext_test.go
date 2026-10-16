package retire_test

import (
	"slices"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// --json names, for each problem, the category that goes past it, and the
// categories given; the result says which it went past.
func TestRemoveJSONNamesTheForceCategories(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/force-json")
	agents := []wtsync.Agent{retire.AgentIn(t, path, "idle-1", true), retire.AgentIn(t, path, "busy-1", false)}
	p, err := retire.RemovePlanOf(t, ctx, path, retire.RemoveOptions{Force: kit.ForceIdleSessions, Agents: agents})
	if err == nil {
		t.Fatal("want a refusal: the busy session is not forced")
	}
	// force keeps its v1 meaning: every problem with force true is gone
	// past. Only bare --force promises that.
	if !slices.Equal(p.ForceWith, []string{"idle-sessions"}) || p.Force {
		t.Errorf("force = %v, forceWith = %q", p.Force, p.ForceWith)
	}
	var with []string
	for _, pr := range p.Problems {
		if !pr.Force || pr.ForceWith == nil {
			t.Fatalf("problem %+v: want force and forceWith", pr)
		}
		with = append(with, *pr.ForceWith)
	}
	if !slices.Equal(with, []string{"idle-sessions", "busy-sessions"}) {
		t.Errorf("forceWith = %q", with)
	}

	opts := retire.RemoveOptions{Force: kit.ForceIdleSessions | kit.ForceBusySessions | kit.ForceLock, Agents: agents}
	plan, err := retire.RemovePlanOf(t, ctx, path, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Expect = retire.Deref(plan.Token)
	r, err := retiretest.RemoveJSON(t, ctx, path, opts)
	if err != nil || r.Outcome != retire.RemoveRemoved {
		t.Fatalf("%v %+v", err, r)
	}
	if !slices.Equal(r.Forced, []string{"idle-sessions", "busy-sessions"}) {
		t.Errorf("forced = %q", r.Forced)
	}
}
