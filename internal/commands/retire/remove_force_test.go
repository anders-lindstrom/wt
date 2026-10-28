package retire

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestParseForce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []string
		want kit.ForceSet
	}{
		{[]string{"all"}, kit.ForceAll},
		{[]string{"idle-sessions"}, kit.ForceIdleSessions},
		{[]string{"idle-sessions,sessions-unknown", "hidden-files"},
			kit.ForceIdleSessions | kit.ForceSessionsUnknown | kit.ForceHiddenFiles},
		// Going past a busy session goes past an idle one too: a session
		// that stops working must not turn the removal into a refusal.
		{[]string{"lock", "busy-sessions"}, kit.ForceLock | kit.ForceBusySessions | kit.ForceIdleSessions},
		{nil, 0},
	} {
		got, err := kit.ParseForce(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseForce(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range [][]string{{"sessions"}, {"idle-sessions,"}, {""}, {"lock,everything"}} {
		_, err := kit.ParseForce(bad)
		if err == nil || !strings.Contains(err.Error(), "idle-sessions") {
			t.Errorf("ParseForce(%q) = %v; want an error listing what --force takes", bad, err)
		}
	}
	if got := (kit.ForceIdleSessions | kit.ForceLock).Names(); !slices.Equal(got, []string{"idle-sessions", "lock"}) {
		t.Errorf("Names = %q", got)
	}
}

// AgentIn is a session working in path: idle, or busy.
func AgentIn(t *testing.T, path, id string, idle bool) agents.Agent {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	a := agents.Agent{ID: id, Name: id, Cwd: resolved, Status: "busy"}
	if idle {
		a.Status = "idle"
	}
	return a
}

// removed runs a removal that must happen.
func removed(t *testing.T, ctx *kit.Context, path string, opts RemoveOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, opts, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the worktree should be gone\n%s", buf.String())
	}
	return buf.String()
}

// Each category goes past its own problem and nothing else's.
func TestRemoveForceCategoryGoesPastOnlyItsOwnProblem(t *testing.T) {
	t.Parallel()
	// busy-sessions implies idle-sessions, so it is no other category's.
	everyOther := func(f kit.ForceSet) kit.ForceSet {
		if f == kit.ForceIdleSessions {
			f |= kit.ForceBusySessions
		}
		return kit.ForceAll &^ f
	}
	for _, tc := range []struct {
		name  string
		force kit.ForceSet
		setup func(t *testing.T, ctx *kit.Context, path string, opts *RemoveOptions)
		why   string
	}{
		{"idle-sessions", kit.ForceIdleSessions, func(t *testing.T, _ *kit.Context, path string, o *RemoveOptions) {
			o.Agents = []agents.Agent{AgentIn(t, path, "idle-1", true)}
		}, "idle-1"},
		{"busy-sessions", kit.ForceBusySessions, func(t *testing.T, _ *kit.Context, path string, o *RemoveOptions) {
			o.Agents = []agents.Agent{AgentIn(t, path, "busy-1", false)}
		}, "busy-1"},
		{"sessions-unknown", kit.ForceSessionsUnknown, func(_ *testing.T, _ *kit.Context, _ string, o *RemoveOptions) {
			o.AgentsErr = errors.New("claude agents --json failed: boom")
		}, "cannot list agent sessions"},
		{"hidden-files", kit.ForceHiddenFiles, func(t *testing.T, _ *kit.Context, path string, _ *RemoveOptions) {
			testkit.MustWrite(t, filepath.Join(path, "a.txt"), "a")
			testkit.GitIn(t, path, "add", "a.txt")
			testkit.GitIn(t, path, "commit", "-q", "-m", "a")
			testkit.GitIn(t, path, "update-index", "--assume-unchanged", "a.txt")
		}, "told not to look at"},
		{"lock", kit.ForceLock, func(t *testing.T, ctx *kit.Context, path string, _ *RemoveOptions) {
			testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", fmt.Sprintf("live (pid %d)", os.Getpid()), path)
		}, "locked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, path := provisiontest.SafetyWorktree(t, "fix/force-"+tc.name)
			opts := RemoveOptions{Agents: []agents.Agent{}}
			tc.setup(t, ctx, path, &opts)
			opts.Force = everyOther(tc.force)
			refused(t, ctx, path, opts, tc.why)
			opts.Force = tc.force
			removed(t, ctx, path, opts)
		})
	}
}

// A refusal --force would go past says which categories it needs.
func TestRemoveRefusalNamesTheForceItNeeds(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/force-hint")
	opts := RemoveOptions{Agents: []agents.Agent{AgentIn(t, path, "busy-1", false)}, Force: kit.ForceIdleSessions}
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "pass --force=idle-sessions,busy-sessions to") {
		t.Fatalf("want the hint naming both categories, got %v\n%s", err, buf.String())
	}
}

// Idle forced, busy not: an idle and a busy session together refuse, and
// the refusal names the busy one.
func TestRemoveIdleForcedButBusyRefuses(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/force-mixed")
	opts := RemoveOptions{Force: kit.ForceIdleSessions, Agents: []agents.Agent{
		AgentIn(t, path, "idle-1", true), AgentIn(t, path, "busy-1", false)}}
	refused(t, ctx, path, opts, "busy-1")
	opts.Force = kit.ForceIdleSessions | kit.ForceBusySessions
	removed(t, ctx, path, opts)
}

// The sessions are listed again right before the checkout goes: one that
// turned busy since the plan refuses unless busy sessions were forced, and
// one that arrived refuses unless its state was forced — deleting or
// quarantining alike.
func TestRemoveReadsTheSessionsAgainBeforeTheCheckoutGoes(t *testing.T) {
	t.Parallel()
	for _, quarantined := range []bool{false, true} {
		name := "delete"
		if quarantined {
			name = "quarantine"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				plan, now func(path string) []agents.Agent
				force     kit.ForceSet
				refuse    bool
			}{
				{"turned busy", idleThen(t, true), busyNow(t), kit.ForceIdleSessions, true},
				{"turned busy, busy forced", idleThen(t, true), busyNow(t), kit.ForceIdleSessions | kit.ForceBusySessions, false},
				{"turned idle, busy forced", busyNow(t), idleThen(t, true), kit.ForceBusySessions, false},
				{"arrived idle", nobody, idleThen(t, true), 0, true},
				{"arrived idle, idle forced", nobody, idleThen(t, true), kit.ForceIdleSessions, false},
				{"arrived busy, idle forced", nobody, busyNow(t), kit.ForceIdleSessions, true},
				{"listing failed", nobody, nil, kit.ForceIdleSessions, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx, path := provisiontest.SafetyWorktree(t, "fix/late-session")
					opts := RemoveOptions{Force: tc.force, Agents: tc.plan(path)}
					opts.Relist = func() ([]agents.Agent, error) {
						if tc.now == nil {
							return nil, errors.New("claude agents --json failed: boom")
						}
						return tc.now(path), nil
					}
					if quarantined {
						opts.Quarantine = testkit.TrashFor(t, ctx)
					}
					var res RemoveResult
					opts.Result = &res
					var buf bytes.Buffer
					err := RemoveAt(ctx, path, opts, &buf)
					_, statErr := os.Stat(path)
					if tc.refuse {
						if err == nil || statErr != nil {
							t.Fatalf("want a refusal with the worktree kept: %v\n%s", err, buf.String())
						}
						if res.Outcome != RemoveRefused {
							t.Errorf("outcome = %q", res.Outcome)
						}
						if quarantined {
							if _, err := os.Stat(opts.Quarantine); !os.IsNotExist(err) {
								t.Error("a refused quarantine must not make its folder")
							}
						}
						return
					}
					if err != nil || statErr == nil {
						t.Fatalf("want it removed: %v\n%s", err, buf.String())
					}
				})
			}
		})
	}
}

func nobody(string) []agents.Agent { return []agents.Agent{} }

func idleThen(t *testing.T, idle bool) func(string) []agents.Agent {
	return func(path string) []agents.Agent { return []agents.Agent{AgentIn(t, path, "s-1", idle)} }
}

func busyNow(t *testing.T) func(string) []agents.Agent { return idleThen(t, false) }

// The token covers each session's state as well as who it is, and which
// categories --force names.
func TestRemoveTokenCoversSessionStateAndTheForceSet(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/force-token")
	token := func(force kit.ForceSet, idle bool) string {
		t.Helper()
		p, err := RemovePlanOf(t, ctx, path, RemoveOptions{Force: force,
			Agents: []agents.Agent{AgentIn(t, path, "s-1", idle)}})
		if err != nil || p.Token == nil {
			t.Fatalf("no token: %v %+v", err, p)
		}
		return *p.Token
	}
	if token(kit.ForceAll, true) == token(kit.ForceAll, false) {
		t.Error("the token must change when the session turns busy")
	}
	if token(kit.ForceAll, true) == token(kit.ForceIdleSessions, true) {
		t.Error("the token must change with the categories --force names")
	}
	if first, again := token(kit.ForceIdleSessions, true), token(kit.ForceIdleSessions, true); first != again {
		t.Error("the same plan must have the same token")
	}
}

// A problem --force cannot pass has no category.
func TestRemoveJSONProblemWithoutForceHasNoCategory(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/force-dirty")
	testkit.MustWrite(t, filepath.Join(path, "scratch.txt"), "mine")
	p, _ := RemovePlanOf(t, ctx, path, RemoveOptions{Force: kit.ForceAll, Agents: []agents.Agent{}})
	if len(p.Problems) != 1 || p.Problems[0].Force || p.Problems[0].ForceWith != nil {
		t.Errorf("problems = %+v", p.Problems)
	}
	if !slices.Equal(p.ForceWith, kit.ForceAll.Names()) || !p.Force {
		t.Errorf("forceWith = %q", p.ForceWith)
	}
}

// Listing the sessions takes time. The checkout is read after it, so a file
// written while claude answered is still seen — in a submodule too, where
// git's own check is forced past.
func TestRemoveReadsTheCheckoutAfterListingTheSessions(t *testing.T) {
	t.Parallel()
	ctx, _, path := provisiontest.SubmoduleWorktree(t, "fix/late-while-listing")
	late := filepath.Join(path, "sm", "late.txt")
	opts := RemoveOptions{Agents: []agents.Agent{}, Relist: func() ([]agents.Agent, error) {
		testkit.MustWrite(t, late, "written while the sessions were listed")
		return []agents.Agent{}, nil
	}}
	refused(t, ctx, path, opts, "changed since the plan was made: dirty")
	if _, err := os.Stat(late); err != nil {
		t.Error("the late file must still be there")
	}
}

// Busy forced covers idle ones in the plan too.
func TestRemoveBusyForcedGoesPastAnIdleSession(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/force-busy-idle")
	removed(t, ctx, path, RemoveOptions{Force: kit.ForceBusySessions, Agents: []agents.Agent{
		AgentIn(t, path, "idle-1", true), AgentIn(t, path, "busy-1", false)}})
}

// A refusal at the last check says the worktree changed since the plan, and
// which --force would go past it now.
func TestRemoveLateRefusalNamesWhatChangedAndItsForce(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/late-hint")
	opts := RemoveOptions{Force: kit.ForceIdleSessions, Agents: idleThen(t, true)(path),
		Relist: func() ([]agents.Agent, error) { return busyNow(t)(path), nil }}
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "changed since the plan") ||
		!strings.Contains(err.Error(), "pass --force=idle-sessions,busy-sessions to remove it anyway") {
		t.Fatalf("got %v\n%s", err, buf.String())
	}
}

// A held lock and a session: the hint names both categories.
func TestRemoveLockAndSessionHintNamesBoth(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/lock-and-session")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", fmt.Sprintf("live (pid %d)", os.Getpid()), path)
	opts := RemoveOptions{Agents: idleThen(t, true)(path)}
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "--force=idle-sessions,lock") {
		t.Fatalf("got %v\n%s", err, buf.String())
	}
}
