package commands

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

func TestParseForce(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want ForceSet
	}{
		{[]string{"all"}, ForceAll},
		{[]string{"idle-sessions"}, ForceIdleSessions},
		{[]string{"idle-sessions,sessions-unknown", "hidden-files"},
			ForceIdleSessions | ForceSessionsUnknown | ForceHiddenFiles},
		// Going past a busy session goes past an idle one too: a session
		// that stops working must not turn the removal into a refusal.
		{[]string{"lock", "busy-sessions"}, ForceLock | ForceBusySessions | ForceIdleSessions},
		{nil, 0},
	} {
		got, err := ParseForce(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseForce(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range [][]string{{"sessions"}, {"idle-sessions,"}, {""}, {"lock,everything"}} {
		_, err := ParseForce(bad)
		if err == nil || !strings.Contains(err.Error(), "idle-sessions") {
			t.Errorf("ParseForce(%q) = %v; want an error listing what --force takes", bad, err)
		}
	}
	if got := (ForceIdleSessions | ForceLock).Names(); !slices.Equal(got, []string{"idle-sessions", "lock"}) {
		t.Errorf("Names = %q", got)
	}
}

// agentIn is a session working in path: idle, or busy.
func agentIn(t *testing.T, path, id string, idle bool) wtsync.Agent {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	a := wtsync.Agent{ID: id, Name: id, Cwd: resolved, Status: "busy"}
	if idle {
		a.Status = "idle"
	}
	return a
}

// removed runs a removal that must happen.
func removed(t *testing.T, ctx *Context, path string, opts RemoveOptions) string {
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
	// busy-sessions implies idle-sessions, so it is no other category's.
	everyOther := func(f ForceSet) ForceSet {
		if f == ForceIdleSessions {
			f |= ForceBusySessions
		}
		return ForceAll &^ f
	}
	for _, tc := range []struct {
		name  string
		force ForceSet
		setup func(t *testing.T, ctx *Context, path string, opts *RemoveOptions)
		why   string
	}{
		{"idle-sessions", ForceIdleSessions, func(t *testing.T, _ *Context, path string, o *RemoveOptions) {
			o.Agents = []wtsync.Agent{agentIn(t, path, "idle-1", true)}
		}, "idle-1"},
		{"busy-sessions", ForceBusySessions, func(t *testing.T, _ *Context, path string, o *RemoveOptions) {
			o.Agents = []wtsync.Agent{agentIn(t, path, "busy-1", false)}
		}, "busy-1"},
		{"sessions-unknown", ForceSessionsUnknown, func(_ *testing.T, _ *Context, _ string, o *RemoveOptions) {
			o.AgentsErr = errors.New("claude agents --json failed: boom")
		}, "cannot list agent sessions"},
		{"hidden-files", ForceHiddenFiles, func(t *testing.T, _ *Context, path string, _ *RemoveOptions) {
			mustWrite(t, filepath.Join(path, "a.txt"), "a")
			gitIn(t, path, "add", "a.txt")
			gitIn(t, path, "commit", "-q", "-m", "a")
			gitIn(t, path, "update-index", "--assume-unchanged", "a.txt")
		}, "told not to look at"},
		{"lock", ForceLock, func(t *testing.T, ctx *Context, path string, _ *RemoveOptions) {
			gitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", fmt.Sprintf("live (pid %d)", os.Getpid()), path)
		}, "locked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, path := safetyWorktree(t, "fix/force-"+tc.name)
			opts := RemoveOptions{Agents: []wtsync.Agent{}}
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
	ctx, path := safetyWorktree(t, "fix/force-hint")
	opts := RemoveOptions{Agents: []wtsync.Agent{agentIn(t, path, "busy-1", false)}, Force: ForceIdleSessions}
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "pass --force=idle-sessions,busy-sessions to") {
		t.Fatalf("want the hint naming both categories, got %v\n%s", err, buf.String())
	}
}

// Idle forced, busy not: an idle and a busy session together refuse, and
// the refusal names the busy one.
func TestRemoveIdleForcedButBusyRefuses(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/force-mixed")
	opts := RemoveOptions{Force: ForceIdleSessions, Agents: []wtsync.Agent{
		agentIn(t, path, "idle-1", true), agentIn(t, path, "busy-1", false)}}
	refused(t, ctx, path, opts, "busy-1")
	opts.Force = ForceIdleSessions | ForceBusySessions
	removed(t, ctx, path, opts)
}

// The sessions are listed again right before the checkout goes: one that
// turned busy since the plan refuses unless busy sessions were forced, and
// one that arrived refuses unless its state was forced — deleting or
// quarantining alike.
func TestRemoveReadsTheSessionsAgainBeforeTheCheckoutGoes(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		name := "delete"
		if quarantined {
			name = "quarantine"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				plan, now func(path string) []wtsync.Agent
				force     ForceSet
				refuse    bool
			}{
				{"turned busy", idleThen(t, true), busyNow(t), ForceIdleSessions, true},
				{"turned busy, busy forced", idleThen(t, true), busyNow(t), ForceIdleSessions | ForceBusySessions, false},
				{"turned idle, busy forced", busyNow(t), idleThen(t, true), ForceBusySessions, false},
				{"arrived idle", nobody, idleThen(t, true), 0, true},
				{"arrived idle, idle forced", nobody, idleThen(t, true), ForceIdleSessions, false},
				{"arrived busy, idle forced", nobody, busyNow(t), ForceIdleSessions, true},
				{"listing failed", nobody, nil, ForceIdleSessions, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx, path := safetyWorktree(t, "fix/late-session")
					opts := RemoveOptions{Force: tc.force, Agents: tc.plan(path)}
					opts.Relist = func() ([]wtsync.Agent, error) {
						if tc.now == nil {
							return nil, errors.New("claude agents --json failed: boom")
						}
						return tc.now(path), nil
					}
					if quarantined {
						opts.Quarantine = trashFor(t, ctx)
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

func nobody(string) []wtsync.Agent { return []wtsync.Agent{} }

func idleThen(t *testing.T, idle bool) func(string) []wtsync.Agent {
	return func(path string) []wtsync.Agent { return []wtsync.Agent{agentIn(t, path, "s-1", idle)} }
}

func busyNow(t *testing.T) func(string) []wtsync.Agent { return idleThen(t, false) }

// The token covers each session's state as well as who it is, and which
// categories --force names.
func TestRemoveTokenCoversSessionStateAndTheForceSet(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/force-token")
	token := func(force ForceSet, idle bool) string {
		t.Helper()
		p, err := removePlanJSON(t, ctx, path, RemoveOptions{Force: force,
			Agents: []wtsync.Agent{agentIn(t, path, "s-1", idle)}})
		if err != nil || p.Token == nil {
			t.Fatalf("no token: %v %+v", err, p)
		}
		return *p.Token
	}
	if token(ForceAll, true) == token(ForceAll, false) {
		t.Error("the token must change when the session turns busy")
	}
	if token(ForceAll, true) == token(ForceIdleSessions, true) {
		t.Error("the token must change with the categories --force names")
	}
	if first, again := token(ForceIdleSessions, true), token(ForceIdleSessions, true); first != again {
		t.Error("the same plan must have the same token")
	}
}

// --json names, for each problem, the category that goes past it, and the
// categories given; the result says which it went past.
func TestRemoveJSONNamesTheForceCategories(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/force-json")
	agents := []wtsync.Agent{agentIn(t, path, "idle-1", true), agentIn(t, path, "busy-1", false)}
	p, err := removePlanJSON(t, ctx, path, RemoveOptions{Force: ForceIdleSessions, Agents: agents})
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

	opts := RemoveOptions{Force: ForceIdleSessions | ForceBusySessions | ForceLock, Agents: agents}
	plan, err := removePlanJSON(t, ctx, path, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Expect = deref(plan.Token)
	r, err := removeJSON(t, ctx, path, opts)
	if err != nil || r.Outcome != RemoveRemoved {
		t.Fatalf("%v %+v", err, r)
	}
	if !slices.Equal(r.Forced, []string{"idle-sessions", "busy-sessions"}) {
		t.Errorf("forced = %q", r.Forced)
	}
}

// A problem --force cannot pass has no category.
func TestRemoveJSONProblemWithoutForceHasNoCategory(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/force-dirty")
	mustWrite(t, filepath.Join(path, "scratch.txt"), "mine")
	p, _ := removePlanJSON(t, ctx, path, RemoveOptions{Force: ForceAll, Agents: []wtsync.Agent{}})
	if len(p.Problems) != 1 || p.Problems[0].Force || p.Problems[0].ForceWith != nil {
		t.Errorf("problems = %+v", p.Problems)
	}
	if !slices.Equal(p.ForceWith, ForceAll.Names()) || !p.Force {
		t.Errorf("forceWith = %q", p.ForceWith)
	}
}

// Listing the sessions takes time. The checkout is read after it, so a file
// written while claude answered is still seen — in a submodule too, where
// git's own check is forced past.
func TestRemoveReadsTheCheckoutAfterListingTheSessions(t *testing.T) {
	ctx, _, path := submoduleWorktree(t, "fix/late-while-listing")
	late := filepath.Join(path, "sm", "late.txt")
	opts := RemoveOptions{Agents: []wtsync.Agent{}, Relist: func() ([]wtsync.Agent, error) {
		mustWrite(t, late, "written while the sessions were listed")
		return []wtsync.Agent{}, nil
	}}
	refused(t, ctx, path, opts, "changed since the plan was made: dirty")
	if _, err := os.Stat(late); err != nil {
		t.Error("the late file must still be there")
	}
}

// Busy forced covers idle ones in the plan too.
func TestRemoveBusyForcedGoesPastAnIdleSession(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/force-busy-idle")
	removed(t, ctx, path, RemoveOptions{Force: ForceBusySessions, Agents: []wtsync.Agent{
		agentIn(t, path, "idle-1", true), agentIn(t, path, "busy-1", false)}})
}

// A refusal at the last check says the worktree changed since the plan, and
// which --force would go past it now.
func TestRemoveLateRefusalNamesWhatChangedAndItsForce(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/late-hint")
	opts := RemoveOptions{Force: ForceIdleSessions, Agents: idleThen(t, true)(path),
		Relist: func() ([]wtsync.Agent, error) { return busyNow(t)(path), nil }}
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "changed since the plan") ||
		!strings.Contains(err.Error(), "pass --force=idle-sessions,busy-sessions to remove it anyway") {
		t.Fatalf("got %v\n%s", err, buf.String())
	}
}

// A held lock and a session: the hint names both categories.
func TestRemoveLockAndSessionHintNamesBoth(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/lock-and-session")
	gitIn(t, ctx.Repo.MainRoot, "worktree", "lock", "--reason", fmt.Sprintf("live (pid %d)", os.Getpid()), path)
	opts := RemoveOptions{Agents: idleThen(t, true)(path)}
	var buf bytes.Buffer
	err := RemoveAt(ctx, path, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "--force=idle-sessions,lock") {
		t.Fatalf("got %v\n%s", err, buf.String())
	}
}
