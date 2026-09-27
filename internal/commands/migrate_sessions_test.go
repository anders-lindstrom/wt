package commands

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Not being able to tell who is in a worktree is not nobody in it: migrate
// refuses, as remove and sweep do, unless told to go ahead.
func TestMigrateRefusesWhenTheSessionsCannotBeListed(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/moving", filepath.Join(ctx.Repo.Parent, "elsewhere"))

	var buf bytes.Buffer
	opts := MigrateOptions{AgentsErr: errors.New("claude agents --json failed: boom")}
	if _, err := Migrate(ctx, from, "", opts, &buf); err == nil || !strings.Contains(err.Error(), "cannot list agent sessions") {
		t.Fatalf("want a refusal naming the failed listing, got %v\n%s", err, buf.String())
	}
	if ctx.Repo.BranchAt(from) != "fix/moving" {
		t.Fatal("nothing may have moved")
	}

	opts.Force = true
	if _, err := Migrate(ctx, from, "", opts, &buf); err != nil {
		t.Fatalf("--force moves past a failed listing: %v\n%s", err, buf.String())
	}
}

// The session running wt migrate is not somebody else in the worktree.
func TestMigrateDoesNotCountTheSessionRunningIt(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/self", filepath.Join(ctx.Repo.Parent, "elsewhere"))
	resolved, _ := filepath.EvalSymlinks(from)
	fakeClaude(t, []wtsync.Agent{ownSession(resolved)})

	var buf bytes.Buffer
	if _, err := Migrate(ctx, from, "", MigrateOptions{}, &buf); err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
}

// Every session in it is named, not only the first.
func TestMigrateCountsEverySessionInIt(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/crowded", filepath.Join(ctx.Repo.Parent, "elsewhere"))
	resolved, _ := filepath.EvalSymlinks(from)
	opts := MigrateOptions{Agents: []wtsync.Agent{
		{Name: "gecko-1", Cwd: resolved, Status: "idle"},
		{Name: "gecko-2", Cwd: filepath.Join(resolved, "sub"), Status: "busy"},
	}}

	var buf bytes.Buffer
	_, err := Migrate(ctx, from, "", opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "gecko-2 +1") {
		t.Fatalf("want a refusal naming both sessions, got %v\n%s", err, buf.String())
	}
}
