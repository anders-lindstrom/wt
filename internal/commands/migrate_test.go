package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// noSessions is the option set every test here starts from: no agent session
// lives in a temporary repository, and asking `claude agents` about it would
// make the test depend on the machine running it.
func noSessions() MigrateOptions { return MigrateOptions{Agents: []wtsync.Agent{}} }

func worktreeAt(t *testing.T, main, branch, path string) string {
	t.Helper()
	gitIn(t, main, "worktree", "add", "-q", "-b", branch, path)
	return path
}

// The case that started this: a worktree Superset made at a uuid path, on a
// branch outside the convention, that has to end up under another type and
// another name.
func TestMigrateRetypesRenamesAndMovesInOneGo(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent,
		"demo_wt", "feat_wt", "5f2c8e10-7d3a-4b6e-9c01-2a4f6b8d0e13", "local-cache"))
	mustWrite(t, filepath.Join(from, "precious.txt"), "real work")

	var buf bytes.Buffer
	got, err := Migrate(ctx, from, "fix/local-cache", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "local-cache")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if branch := ctx.Repo.BranchAt(want); branch != "fix_wt/local-cache" {
		t.Errorf("branch = %q, want fix_wt/local-cache", branch)
	}
	if body := mustRead(t, filepath.Join(want, "precious.txt")); body != "real work" {
		t.Errorf("the work did not travel: %q", body)
	}
}

// A branch with no type at all — Superset mints these — is fitted to the
// convention without the user having to say anything twice.
func TestMigrateFitsATypelessBranchToTheConvention(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "api_tidy", filepath.Join(ctx.Repo.Parent,
		"demo_wt", "demo", "feat_wt", "new_parser"))

	var buf bytes.Buffer
	got, err := Migrate(ctx, "api_tidy", "", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "api_tidy")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if branch := ctx.Repo.BranchAt(want); branch != "feat_wt/api_tidy" {
		t.Errorf("branch = %q, want feat_wt/api_tidy", branch)
	}
}

// A branch that is <known type>/<name> already says which type it is; the
// slash is just missing the suffix.
func TestMigrateReadsTheTypeOutOfANonConventionBranch(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))

	var buf bytes.Buffer
	got, err := Migrate(ctx, "fix/flaky-test", "", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "flaky-test")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The work name, the branch and the path are the three columns `wt list`
// prints; all three must name the same worktree.
func TestMigrateAcceptsEveryWayAWorktreeIsPrinted(t *testing.T) {
	for _, how := range []string{"work", "branch", "path"} {
		t.Run(how, func(t *testing.T) {
			main := committedRepo(t, minimalConf)
			ctx, _ := Open(main)
			path := worktreeAt(t, main, "feat_wt/cache_stats",
				filepath.Join(ctx.Repo.Parent, "demo-cache_stats"))
			arg := map[string]string{
				"work": "cache_stats", "branch": "feat_wt/cache_stats", "path": path,
			}[how]

			var buf bytes.Buffer
			got, err := Migrate(ctx, arg, "", noSessions(), &buf)
			if err != nil {
				t.Fatalf("Migrate: %v\n%s", err, buf.String())
			}
			want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "cache_stats")
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// A destination with no type keeps the type the worktree already has: renaming
// is not retyping.
func TestMigrateKeepsTheTypeWhenOnlyTheNameChanges(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "perf_wt/prefetch", filepath.Join(ctx.Repo.Parent, "demo-prefetch"))

	var buf bytes.Buffer
	got, err := Migrate(ctx, "prefetch", "prefilter", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "perf_wt", "prefilter")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if branch := ctx.Repo.BranchAt(want); branch != "perf_wt/prefilter" {
		t.Errorf("branch = %q, want perf_wt/prefilter", branch)
	}
}

// The branch form of a destination is the same answer typed differently.
func TestMigrateAcceptsADestinationWrittenAsABranch(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "feat_wt/login", filepath.Join(ctx.Repo.Parent, "demo-login"))

	var buf bytes.Buffer
	got, err := Migrate(ctx, "login", "chore_wt/login", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "chore_wt", "login")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The plan comes before the move, so a user can read what is about to happen
// to a worktree carrying work.
func TestMigratePrintsWhatItWillDoBeforeDoingIt(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))

	var buf bytes.Buffer
	if _, err := Migrate(ctx, from, "fix/local-cache", noSessions(), &buf); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	out := buf.String()
	for _, want := range []string{from, "fix/flaky-test", "fix_wt/local-cache",
		filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "local-cache")} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan never mentions %q:\n%s", want, out)
		}
	}
}

// Uncommitted work is not a reason to refuse: git's own move carries it.
func TestMigrateMovesADirtyWorktreeAndSaysSo(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "feat_wt/dirty", filepath.Join(ctx.Repo.Parent, "demo-dirty"))
	mustWrite(t, filepath.Join(from, "tracked.txt"), "committed")
	gitIn(t, from, "add", "tracked.txt")
	gitIn(t, from, "commit", "-q", "-m", "add tracked")
	mustWrite(t, filepath.Join(from, "tracked.txt"), "edited, not committed")

	var buf bytes.Buffer
	got, err := Migrate(ctx, "dirty", "", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	if body := mustRead(t, filepath.Join(got, "tracked.txt")); body != "edited, not committed" {
		t.Errorf("the edit did not travel: %q", body)
	}
	if !strings.Contains(buf.String(), "uncommitted changes") {
		t.Errorf("the plan should say the checkout is dirty:\n%s", buf.String())
	}
}

// The message a user gets for an argument that names nothing has to say what
// was looked for and where to look.
func TestMigrateSaysWhatItLookedForWhenNothingMatches(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	_, err := Migrate(ctx, "nosuchthing", "", noSessions(), &buf)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "nosuchthing") || !strings.Contains(err.Error(), "wt list") {
		t.Errorf("unhelpful message: %q", err)
	}
}

// The old command computed a path from the argument and then read the branch
// of a directory that did not exist, which is where `is on ""` came from.
func TestMigrateNeverBlamesAnEmptyBranch(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))

	for _, arg := range []string{"local-cache", "fix/flaky-test",
		filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "5f2c8e10", "local-cache")} {
		var buf bytes.Buffer
		_, err := Migrate(ctx, arg, "", noSessions(), &buf)
		msg := buf.String()
		if err != nil {
			msg += err.Error()
		}
		if strings.Contains(msg, `is on ""`) || strings.Contains(msg, "too many slashes") {
			t.Errorf("%s: %s", arg, msg)
		}
	}
}

// A detached checkout has no branch to rename and nothing to name a path
// from. Say that, and say what to do about it.
func TestMigrateExplainsADetachedWorktree(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	loose := filepath.Join(ctx.Repo.Parent, "demo-loose")
	gitIn(t, main, "worktree", "add", "-q", "--detach", loose)

	var buf bytes.Buffer
	_, err := Migrate(ctx, loose, "fix/whatever", noSessions(), &buf)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "detached") || !strings.Contains(err.Error(), "switch") {
		t.Errorf("unhelpful message: %q", err)
	}
}

func TestMigrateRefusesWhenTheTargetBranchIsTaken(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "feat_wt/login", filepath.Join(ctx.Repo.Parent, "demo-login"))
	worktreeAt(t, main, "fix_wt/login", filepath.Join(ctx.Repo.Parent, "demo-other"))

	var buf bytes.Buffer
	_, err := Migrate(ctx, "feat/login", "fix/login", noSessions(), &buf)
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "fix_wt/login") {
		t.Errorf("the refusal must name the branch in the way: %q", err)
	}
	if branch := ctx.Repo.BranchAt(from); branch != "feat_wt/login" {
		t.Errorf("the branch was renamed anyway: %q", branch)
	}
}

func TestMigrateRefusesWhenAnotherWorktreeIsAtTheTarget(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))
	occupied := worktreeAt(t, main, "fix_wt/taken",
		filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "taken"))

	var buf bytes.Buffer
	_, err := Migrate(ctx, "fix/flaky-test", "fix/taken", noSessions(), &buf)
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), occupied) {
		t.Errorf("the refusal must name what is already there: %q", err)
	}
}

// Moving a directory out from under a running session breaks it, so say whose
// session it is rather than doing it.
func TestMigrateRefusesWhenAnAgentSessionLivesThere(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))
	resolved, _ := filepath.EvalSymlinks(from)
	opts := MigrateOptions{Agents: []wtsync.Agent{{Name: "gecko-1", Cwd: resolved}}}

	var buf bytes.Buffer
	_, err := Migrate(ctx, from, "", opts, &buf)
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "gecko-1") {
		t.Errorf("the refusal must name the session: %q", err)
	}
	if _, err := os.Stat(from); err != nil {
		t.Error("the worktree must be left where it is")
	}
}

func TestMigrateForceMovesPastAnAgentSession(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))
	resolved, _ := filepath.EvalSymlinks(from)
	opts := MigrateOptions{Force: true, Agents: []wtsync.Agent{{Name: "gecko-1", Cwd: resolved}}}

	var buf bytes.Buffer
	got, err := Migrate(ctx, from, "", opts, &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	if got != filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "flaky-test") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(buf.String(), "gecko-1") {
		t.Errorf("--force should still say whose session is in it:\n%s", buf.String())
	}
}

// A move that fails must leave the branch name alone: half a migration is
// worse than none, because nothing names the worktree any more.
func TestMigratePutsTheBranchNameBackWhenTheMoveFails(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))
	// A file where the type directory has to be: mkdir cannot make the
	// destination, so the move fails after the rename.
	mustWrite(t, filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt"), "not a directory")

	var buf bytes.Buffer
	if _, err := Migrate(ctx, from, "", noSessions(), &buf); err == nil {
		t.Fatal("want an error")
	}
	if branch := ctx.Repo.BranchAt(from); branch != "fix/flaky-test" {
		t.Errorf("branch = %q, want the original fix/flaky-test back", branch)
	}
}

func TestMigrateIsANoOpWhenAlreadyWhereItBelongs(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "fix/already", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Migrate(ctx, "fix/already", "", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got != path {
		t.Errorf("got %q, want unchanged %q", got, path)
	}
}

// A branch outside the convention at a path that is already canonical for its
// new name still needs the rename.
func TestMigrateRenamesTheBranchWhenOnlyTheNameIsWrong(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	at := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "api_tidy")
	worktreeAt(t, main, "api_tidy", at)

	var buf bytes.Buffer
	got, err := Migrate(ctx, "api_tidy", "", noSessions(), &buf)
	if err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	if got != at {
		t.Errorf("got %q, want unchanged %q", got, at)
	}
	if branch := ctx.Repo.BranchAt(at); branch != "feat_wt/api_tidy" {
		t.Errorf("branch = %q, want feat_wt/api_tidy", branch)
	}
}

// Superset stores the absolute path of every workspace it makes, so a migrate
// silently breaks the workspace. The warning has to arrive before the move —
// and therefore during a dry run, which is where it is read.
func TestMigrateWarnsBeforeMovingASupersetWorktree(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "fix_wt/legacy",
		filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "fix_wt", "legacy"))

	var buf bytes.Buffer
	opts := noSessions()
	opts.DryRun = true
	if _, err := Migrate(ctx, "fix/legacy", "", opts, &buf); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Superset") {
		t.Errorf("want the workspace warning:\n%s", out)
	}
	if strings.Index(out, "Superset") > strings.Index(out, "would move") {
		t.Errorf("the warning must come before what it warns about:\n%s", out)
	}
}

// --dry-run must show exactly what would happen and change nothing. This is the
// safety valve for worktrees carrying real work: you look before you leap.
func TestMigrateDryRunChangesNothing(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	legacy := worktreeAt(t, main, "fix/legacy", filepath.Join(ctx.Repo.Parent, "demo-legacy"))
	mustWrite(t, filepath.Join(legacy, "precious.txt"), "real work")

	var buf bytes.Buffer
	opts := noSessions()
	opts.DryRun = true
	got, err := Migrate(ctx, "fix/legacy", "fix/renamed", opts, &buf)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "renamed")
	if got != want {
		t.Errorf("dry run should still report the destination: %q", got)
	}
	if _, err := os.Stat(filepath.Join(legacy, "precious.txt")); err != nil {
		t.Error("dry run moved the worktree")
	}
	if _, err := os.Stat(want); err == nil {
		t.Error("dry run created the destination")
	}
	if ctx.Repo.BranchAt(legacy) != "fix/legacy" {
		t.Error("dry run renamed the branch")
	}
	if !strings.Contains(buf.String(), "would move") {
		t.Errorf("dry run should say what it would do:\n%s", buf.String())
	}
}

// A move must be verified against git afterwards, not assumed from the argument.
func TestMigrateReportsWhereTheWorktreeActuallyWent(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "fix_wt/legacy", filepath.Join(ctx.Repo.Parent, "demo-legacy"))

	var buf bytes.Buffer
	got, err := Migrate(ctx, "fix/legacy", "", noSessions(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, wt := range worktrees {
		if wt.Branch == "fix_wt/legacy" {
			found = true
			if wt.Path != got {
				t.Errorf("reported %q but git says %q", got, wt.Path)
			}
		}
	}
	if !found {
		t.Error("git lost track of the worktree")
	}
}

func TestMigrateRefusesWhenDestinationOccupied(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	legacy := worktreeAt(t, main, "fix_wt/legacy", filepath.Join(ctx.Repo.Parent, "demo-legacy"))
	mustWrite(t, filepath.Join(legacy, "precious.txt"), "real work")
	occupied := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "legacy")
	mustWrite(t, filepath.Join(occupied, "someone-elses.txt"), "do not clobber")

	var buf bytes.Buffer
	if _, err := Migrate(ctx, "fix/legacy", "", noSessions(), &buf); err == nil {
		t.Fatal("want a refusal")
	}
	if _, err := os.Stat(filepath.Join(legacy, "precious.txt")); err != nil {
		t.Error("the worktree must be left alone on refusal")
	}
	if got := mustRead(t, filepath.Join(occupied, "someone-elses.txt")); got != "do not clobber" {
		t.Error("the destination must be left alone on refusal")
	}
}

// An unknown type is a typo, not a new type: name what this repository has.
func TestMigrateRefusesAnUnknownType(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	worktreeAt(t, main, "feat_wt/login", filepath.Join(ctx.Repo.Parent, "demo-login"))

	var buf bytes.Buffer
	_, err := Migrate(ctx, "login", "wibble/login", noSessions(), &buf)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "wibble") || !strings.Contains(err.Error(), "feat") {
		t.Errorf("the message must name the types this repo has: %q", err)
	}
}

// A dry run is a preview, so an agent session is something it reports rather
// than something it fails on.
func TestMigrateDryRunReportsTheSessionItWouldRefuseFor(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))
	resolved, _ := filepath.EvalSymlinks(from)
	opts := MigrateOptions{DryRun: true, Agents: []wtsync.Agent{{Name: "gecko-1", Cwd: resolved}}}

	var buf bytes.Buffer
	if _, err := Migrate(ctx, from, "", opts, &buf); err != nil {
		t.Fatalf("a dry run must not fail: %v", err)
	}
	if !strings.Contains(buf.String(), "would refuse") || !strings.Contains(buf.String(), "gecko-1") {
		t.Errorf("the dry run should say a real run would refuse:\n%s", buf.String())
	}
}

// The shell that ran the command is left in a directory that no longer exists.
func TestMigrateSaysWhenYouAreStandingInIt(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	from := worktreeAt(t, main, "fix/flaky-test", filepath.Join(ctx.Repo.Parent, "demo-idiot"))
	t.Chdir(from)

	var buf bytes.Buffer
	if _, err := Migrate(ctx, from, "", noSessions(), &buf); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !strings.Contains(buf.String(), "wt cd flaky-test") {
		t.Errorf("no way back is offered:\n%s", buf.String())
	}
}

// The uuid directory a worktree was nested in is left behind empty by git's
// move, and a husk in the tree is what "not a layout wt recognises" looked
// like in the first place.
func TestMigrateClearsTheEmptyDirectoriesItLeavesBehind(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	nest := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "5f2c8e10-7d3a")
	worktreeAt(t, main, "fix/flaky-test", filepath.Join(nest, "local-cache"))

	var buf bytes.Buffer
	if _, err := Migrate(ctx, "fix/flaky-test", "fix/local-cache", noSessions(), &buf); err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(nest); !os.IsNotExist(err) {
		t.Errorf("%s is still there", nest)
	}
	if _, err := os.Stat(filepath.Join(ctx.Repo.Parent, "demo_wt")); err != nil {
		t.Errorf("the layout root must survive: %v", err)
	}
}

// Only empty ones, and never anything above the repository's own tree.
func TestMigrateLeavesADirectoryThatStillHoldsSomething(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, _ := Open(main)
	nest := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt")
	worktreeAt(t, main, "api_tidy", filepath.Join(nest, "new_parser"))
	keep := filepath.Join(nest, "notes.txt")
	mustWrite(t, keep, "somebody else's")

	var buf bytes.Buffer
	if _, err := Migrate(ctx, "api_tidy", "", noSessions(), &buf); err != nil {
		t.Fatalf("Migrate: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a directory holding something must be left alone: %v", err)
	}
}
