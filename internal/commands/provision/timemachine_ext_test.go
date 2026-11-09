package provision_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// tmRepo is a committed repository on a Mac with a fake tmutil, its
// warnings in the returned buffer.
func tmRepo(t *testing.T, failing bool) (*kit.Context, *testkit.Tmutil, *bytes.Buffer) {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var warned bytes.Buffer
	ctx.WarnTo(&warned)
	return ctx, testkit.FakeTmutil(t, ctx, failing), &warned
}

// wt new excludes <repo>_wt/ from Time Machine, with or without
// provisioning: the folder is rebuildable whatever was set up in it.
func TestNewExcludesTheWorktreeFolderFromTimeMachine(t *testing.T) {
	for name, opts := range map[string]provision.NewOptions{
		"provisioned": {},
		"--no-setup":  {NoSetup: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, tm, _ := tmRepo(t, false)
			var errs bytes.Buffer
			path, err := provision.New(ctx, "fix/login-crash", opts, &errs)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			container := filepath.Join(ctx.Repo.Parent, "demo_wt")
			if got := tm.Excluded(t); !slices.Equal(got, []string{container}) {
				t.Errorf("excluded %q, want %s", got, container)
			}
			if !strings.Contains(errs.String(), "✓ excluded "+container+" from Time Machine") {
				t.Errorf("stderr = %q", errs.String())
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("worktree not created: %v", err)
			}
		})
	}
}

// A tmutil that fails costs one warning line: the worktree is made and the
// command succeeds.
func TestNewSurvivesAFailingTmutil(t *testing.T) {
	ctx, _, warned := tmRepo(t, true)
	path, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, io.Discard)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(warned.String()), "\n"); len(lines) != 1 ||
		!strings.Contains(lines[0], "not excluded from Time Machine") {
		t.Errorf("warned %q, want one line", warned)
	}
}

// Switched off, wt new runs no tmutil.
func TestNewSwitchedOffRunsNoTmutil(t *testing.T) {
	ctx, tm, warned := tmRepo(t, false)
	testkit.TimeMachineSetting(t, ctx, "false")
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, io.Discard); err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := tm.Calls(t); len(got) != 0 {
		t.Errorf("tmutil was called %q", got)
	}
	if warned.Len() != 0 {
		t.Errorf("warned %q", warned)
	}
}

// wt checkout puts its worktree under <repo>_wt/ too.
func TestCheckoutExcludesTheWorktreeFolderFromTimeMachine(t *testing.T) {
	ctx, tm, _ := tmRepo(t, false)
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "feature/pr-123")
	if _, err := provision.Checkout(ctx, "feature/pr-123", "", provision.NewOptions{NoSetup: true}, io.Discard); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if got, want := tm.Excluded(t), filepath.Join(ctx.Repo.Parent, "demo_wt"); !slices.Equal(got, []string{want}) {
		t.Errorf("excluded %q, want %s", got, want)
	}
}

// wt setup in a worktree another tool made — Superset's, inside <repo>_wt/,
// or one anywhere else — excludes it the way wt new would have.
func TestSetupExcludesAWorktreeAnotherToolMade(t *testing.T) {
	ctx, tm, _ := tmRepo(t, false)
	superset := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "theirs")
	elsewhere := filepath.Join(ctx.Repo.Parent, "t3", "demo", "t3-1a2b")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "feat_wt/theirs", superset)
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "t3-1a2b", elsewhere)
	for _, path := range []string{superset, elsewhere} {
		if err := provision.Setup(ctx, path, provision.SetupOptions{Source: "superset"}, io.Discard); err != nil {
			t.Fatalf("Setup %s: %v", path, err)
		}
	}
	want := []string{filepath.Join(ctx.Repo.Parent, "demo_wt"), elsewhere}
	if got := tm.Excluded(t); !slices.Equal(got, want) {
		t.Errorf("excluded %q, want %q", got, want)
	}
}

func TestNewExcludesAWorktreeLeftByAFailingHook(t *testing.T) {
	// Not parallel: fake tmutil changes PATH and FindTmutil.
	for _, json := range []bool{false, true} {
		t.Run(fmt.Sprint(json), func(t *testing.T) {
			ctx, tm, _ := tmRepo(t, false)
			gittest.WriteExecutable(t, filepath.Join(ctx.Repo.MainRoot, ".git", "hooks", "post-checkout"), "#!/bin/sh\necho 'hook says no' >&2\nexit 1\n")
			var err error
			if json {
				err = provision.NewJSON(ctx, "fix/left", provision.NewOptions{}, "", provision.NewCreateJournal(io.Discard, "new"), io.Discard)
			} else {
				_, err = provision.New(ctx, "fix/left", provision.NewOptions{}, io.Discard)
			}
			if err == nil || !strings.Contains(err.Error(), "hook says no") {
				t.Fatalf("original hook error lost: %v", err)
			}
			if got := tm.Excluded(t); !slices.Equal(got, []string{ctx.Scheme().Container()}) {
				t.Error("worktree left by failing hook was not excluded")
			}
		})
	}
}
