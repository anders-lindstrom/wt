package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// keepDay is the day the test clock runs on: not today, so a time printed
// against the real clock carries the date.
var keepDay = time.Date(2025, 3, 4, 14, 20, 0, 0, time.Local)

// keepOpts is a keeper pass with no sessions anywhere and a clock that moves
// a second per reading (a run's safety ref is named by its epoch, so two
// passes at one instant would collide).
func keepOpts(push PushMode) KeepOptions {
	tick := 0
	return KeepOptions{
		Push: push,
		verbOptions: verbOptions{
			Agents: []wtsync.Agent{},
			Now: func() time.Time {
				tick++
				return keepDay.Add(time.Duration(tick-1) * time.Second)
			},
		},
	}
}

// onPlatform pins the platform the keeper's start, stop and status see for
// the length of a test, whatever machine runs it.
func onPlatform(t *testing.T, goos string) {
	t.Helper()
	was := keepGOOS
	keepGOOS = goos
	t.Cleanup(func() { keepGOOS = was })
}

func keepFiles(t *testing.T, ctx *Context) (log, state string) {
	t.Helper()
	_, log, state, err := keepPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return log, state
}

func readKeep(t *testing.T, ctx *Context) keepState {
	t.Helper()
	_, statePath := keepFiles(t, ctx)
	st, ok, err := readKeepState(statePath)
	if err != nil || !ok {
		t.Fatalf("state file: ok %v err %v", ok, err)
	}
	return st
}

// A pass rebases the ready worktree, pushes it, and leaves the log and the
// json behind: the json's trunk is the tip the pass saw.
func TestSyncKeepRunRebasesAReadyWorktreeAndRecordsThePass(t *testing.T) {
	ctx, bump := runFixture(t, false)
	var out bytes.Buffer
	if err := SyncKeepRun(ctx, keepOpts(PushAlways), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"wt sync keep once  origin/main ", "→", "every ready worktree: bump\n", "✓ rebased 1 commit", "✓ pushed feat_wt/bump", "kept: rebased 1, pushed 1, left 0 · log "} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if !gitAncestor(t, ctx.Repo.MainRoot, "origin/main", "feat_wt/bump") {
		t.Fatal("bump is not on trunk")
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", "refs/remotes/origin/feat_wt/bump") != gitOut(t, bump, "rev-parse", "HEAD") {
		t.Fatal("bump was not pushed")
	}
	logPath, _ := keepFiles(t, ctx)
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2025-03-04 14:20:00  origin/main ", "\n  rebased  bump\n", "\n  pushed   bump\n", "\n  left     none\n", "\n  result   rebased 1, pushed 1, left 0\n", "\n    wt sync run  onto origin/main"} {
		if !strings.Contains(string(logged), want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
	st := readKeep(t, ctx)
	if st.TrunkSHA != gitOut(t, ctx.Repo.MainRoot, "rev-parse", "origin/main") || st.Result != "rebased 1, pushed 1, left 0" || st.PID != 0 {
		t.Fatalf("state %+v", st)
	}
	if st.Interval != "30m" || !st.NextRun.Equal(st.LastRun.Add(30*time.Minute)) {
		t.Fatalf("next run %+v", st)
	}
}

// A keeper has nobody to ask: a worktree with a session in it, even an idle
// one a run would ask about, is left alone and named with the session.
func TestSyncKeepRunLeavesAWorktreeWithAnIdleSessionAlone(t *testing.T) {
	ctx, bump := runFixture(t, false)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	opts := keepOpts(PushAlways)
	opts.Agents = idleIn(t, bump, "parked")
	opts.Confirm = func([]string) (bool, error) { t.Fatal("a keeper asked a question"); return false, nil }
	var out bytes.Buffer
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("nothing to do is not an error: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"nothing is ready to rebase\n", "left as they are: bump recipe, session parked (idle)\n", "kept: nothing to rebase, left 1"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("bump moved under the session")
	}
}

// Trunk where the last pass left it is nothing to do: one line, one log
// entry, no run.
func TestSyncKeepRunDoesNothingWhileTrunkIsUnchanged(t *testing.T) {
	ctx, _ := runFixture(t, false)
	opts := keepOpts(PushAlways) // one clock across the passes
	var out bytes.Buffer
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	sha := gitOut(t, ctx.Repo.MainRoot, "rev-parse", "origin/main")
	if want := "wt sync keep once  origin/main " + sha[:7] + " unchanged; nothing to do\n"; out.String() != want {
		t.Fatalf("second pass printed %q, want %q", out.String(), want)
	}
	logPath, _ := keepFiles(t, ctx)
	logged, _ := os.ReadFile(logPath)
	if !strings.HasSuffix(string(logged), "  origin/main "+sha[:7]+" unchanged; nothing to do\n") {
		t.Fatalf("log:\n%s", logged)
	}
	if st := readKeep(t, ctx); st.Result != "trunk unchanged" {
		t.Fatalf("state %+v", st)
	}
	// Trunk moves: the next pass runs again.
	writeFile(t, ctx.Repo.MainRoot, "b.txt", "b\n")
	gitOut(t, ctx.Repo.MainRoot, "add", "-A")
	gitOut(t, ctx.Repo.MainRoot, "commit", "-q", "-m", "trunk again")
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "(fetched)\nwt sync run  onto origin/main") {
		t.Fatalf("trunk moved and the pass did not run:\n%s", out.String())
	}
}

// Two keepers never overlap: a pass holds the lock for its length, and a
// pass that finds it held exits saying so, naming the pid the json records,
// touching nothing. The json alone holds nobody: a pid left behind by an
// interrupted pass, alive again as some other process, does not block.
func TestSyncKeepRunRefusesToOverlapALivePass(t *testing.T) {
	ctx, bump := runFixture(t, false)
	gitDir, _, statePath, err := keepPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeKeepState(statePath, keepState{PID: os.Getpid(), Started: keepDay.Add(-20 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	lock, held, err := acquireKeepLock(gitDir)
	if err != nil || held {
		t.Fatalf("lock: %v, held %v", err, held)
	}
	old := gitOut(t, bump, "rev-parse", "HEAD")
	opts := keepOpts(PushAlways)
	var out bytes.Buffer
	err = SyncKeepRun(ctx, opts, &out)
	if err == nil || err.Error() != "a keeper pass is already running (pid "+strconv.Itoa(os.Getpid())+" since 14:00); nothing rebased" {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("bump moved under a live pass")
	}
	// The lock released, the pid in the json is this very test, alive and
	// not a keeper: it holds nothing, and the pass goes ahead.
	_ = lock.Close()
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("a stale pid held the keeper: %v\n%s", err, out.String())
	}
}

// Two passes started at the same moment: exactly one runs.
func TestSyncKeepRunStartedTwiceAtOnceRunsOnce(t *testing.T) {
	ctx, _ := runFixture(t, false)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	outs := make([]lockedBuffer, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = SyncKeepRun(ctx, keepOpts(PushAlways), &outs[i])
		}()
	}
	wg.Wait()
	refused, ran := 0, 0
	for i, err := range errs {
		switch {
		case err == nil && strings.Contains(outs[i].String(), "rebased 1 commit"):
			ran++
		case err != nil && strings.Contains(err.Error(), "a keeper pass is already running"):
			refused++
		default:
			t.Errorf("pass %d: err %v\n%s", i, err, outs[i].String())
		}
	}
	if ran != 1 || refused != 1 {
		t.Fatalf("%d ran, %d refused; want one of each", ran, refused)
	}
}

// An unattended pass has to know who is in a worktree. Without a claude to
// ask it rebases nothing, says so, and records that as the problem.
func TestSyncKeepRunRebasesNothingWhenSessionsCannotBeListed(t *testing.T) {
	ctx, bump := runFixture(t, false)
	// A PATH with git and nothing else on it: no claude, wherever this runs.
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	old := gitOut(t, bump, "rev-parse", "HEAD")
	opts := keepOpts(PushAlways)
	opts.Agents = nil
	var out bytes.Buffer
	err = SyncKeepRun(ctx, opts, &out)
	want := "cannot list agent sessions (claude is not on the PATH); an unattended pass needs them, nothing rebased"
	if err == nil || err.Error() != want {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("bump moved with nobody able to say who was in it")
	}
	st := readKeep(t, ctx)
	if st.TrunkSHA != "" || st.Problem != "failed: "+want {
		t.Fatalf("state %+v", st)
	}
	logPath, _ := keepFiles(t, ctx)
	if logged, _ := os.ReadFile(logPath); !strings.Contains(string(logged), "    "+want+"\n") {
		t.Fatalf("log:\n%s", logged)
	}
}

// A log that cannot be written fails the pass before anything moves: the
// log is the record a background job promises.
func TestSyncKeepRunFailsWhenTheLogCannotBeWritten(t *testing.T) {
	ctx, bump := runFixture(t, false)
	logPath, _ := keepFiles(t, ctx)
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		t.Fatal(err)
	}
	old := gitOut(t, bump, "rev-parse", "HEAD")
	var out bytes.Buffer
	err := SyncKeepRun(ctx, keepOpts(PushAlways), &out)
	if err == nil || !strings.HasPrefix(err.Error(), "cannot write the log "+logPath+": ") || !strings.HasSuffix(err.Error(), "; nothing rebased") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if gitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("bump moved with no log to say so")
	}
	if st := readKeep(t, ctx); st.TrunkSHA != "" || !strings.HasPrefix(st.Problem, "failed: cannot write the log: ") {
		t.Fatalf("state %+v", st)
	}
}

// The log is written as the pass goes: a pass interrupted in the middle of
// a slow deferred step leaves everything up to the interrupt, and the way
// back, on disk, where a launchd job with its stdout on /dev/null has
// nothing else.
func TestSyncKeepRunInterruptedLeavesTheOutputSoFarInTheLog(t *testing.T) {
	if os.Getenv(interruptChild) == "" {
		out, status := interruptedChild(t)
		_, rest, ok := strings.Cut(out, "LOG=")
		logPath, _, _ := strings.Cut(rest, "\n")
		if !ok || status != interruptStatus {
			t.Fatalf("status %d, log path %q:\n%s", status, logPath, out)
		}
		logged, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		for _, want := range []string{"    wt sync run  onto origin/main", "      ✓ rebased 1 commit onto origin/main", "interrupted after rebasing bump"} {
			if !strings.Contains(string(logged), want) {
				t.Errorf("log lacks %q:\n%s", want, logged)
			}
		}
		return
	}
	ctx, _ := runFixture(t, false)
	main := ctx.Repo.MainRoot
	started := filepath.Join(t.TempDir(), "started")
	yaml, err := os.ReadFile(filepath.Join(main, ".wt-sync.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, main, ".wt-sync.yaml", string(yaml)+"defer:\n  - run: touch "+started+"; sleep 30\n    paths: [v.txt]\n")
	gitIn(t, main, "commit", "-q", "-am", "declare a slow step")
	gitIn(t, main, "fetch", "-q", "origin")
	logPath, _ := keepFiles(t, ctx)
	fmt.Println("LOG=" + logPath)
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	err = SyncKeepRun(ctx, keepOpts(PushAlways), os.Stdout)
	t.Fatalf("the pass returned (%v) instead of being interrupted", err)
}

// --no-push rebases and prints the push command for each worktree that
// finished, into the log when the job runs it.
func TestSyncKeepRunNoPushPrintsThePushCommands(t *testing.T) {
	ctx, bump := runFixture(t, false)
	var out bytes.Buffer
	if err := SyncKeepRun(ctx, keepOpts(PushNever), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "push: git -C "+bump+" push --force-with-lease --force-if-includes") {
		t.Fatalf("out:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "kept: rebased 1, pushed 0, left 0, unpushed 1") {
		t.Fatalf("out:\n%s", out.String())
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", "refs/remotes/origin/feat_wt/bump") == gitOut(t, bump, "rev-parse", "HEAD") {
		t.Fatal("bump was pushed under --no-push")
	}
}

// Under --no-push the push is a person's, and the keeper keeps reminding:
// the command is printed again every pass, trunk moved or not, until origin
// has the tip, after which the pass has nothing to do again.
func TestSyncKeepRunNoPushRepeatsThePushCommandUntilItIsPushedByHand(t *testing.T) {
	ctx, bump := runFixture(t, false)
	opts := keepOpts(PushNever)
	var out bytes.Buffer
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	st := readKeep(t, ctx)
	if len(st.Unpushed) != 1 || st.Unpushed[0].Work != "bump" || st.Unpushed[0].Tip != gitOut(t, bump, "rev-parse", "HEAD") || st.Problem != "" {
		t.Fatalf("state %+v", st)
	}
	sha := gitOut(t, ctx.Repo.MainRoot, "rev-parse", "origin/main")
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"wt sync keep once  origin/main " + sha[:7] + " unchanged; 1 push to retry\n", "push: git -C " + bump + " push --force-with-lease --force-if-includes", "kept: trunk unchanged, pushed 0, unpushed 1 · log "} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "wt sync run  onto") {
		t.Fatalf("trunk unchanged, yet the run ran:\n%s", s)
	}
	out.Reset()
	if err := SyncKeepStatus(ctx, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\nunpushed bump\n") {
		t.Fatalf("status:\n%s", out.String())
	}
	gitOut(t, bump, "push", "-q", "--force-with-lease", "origin", "feat_wt/bump")
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if want := "wt sync keep once  origin/main " + sha[:7] + " unchanged; nothing to do\n"; out.String() != want {
		t.Fatalf("pushed by hand, yet: %q", out.String())
	}
	if st := readKeep(t, ctx); len(st.Unpushed) != 0 {
		t.Fatalf("state %+v", st)
	}
}

// The overview says when the repository was last kept, and the doctor has
// a keeper row; neither says anything about a repository never kept.
func TestSyncTableAndDoctorSayWhenTheRepositoryWasKept(t *testing.T) {
	ctx, _ := runFixture(t, false)
	t.Setenv("HOME", t.TempDir())
	onPlatform(t, "darwin")
	var out bytes.Buffer
	if err := Sync(ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "kept") {
		t.Fatalf("never kept, yet:\n%s", out.String())
	}
	out.Reset()
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "keeper       ok     not installed; wt sync keep start\n") {
		t.Fatalf("doctor:\n%s", out.String())
	}

	out.Reset()
	if err := SyncKeepRun(ctx, keepOpts(PushAlways), &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	out.Reset()
	if err := Sync(ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "against origin/main ") || lines[1] != "kept at Mar 4 14:20 · wt sync keep status" {
		t.Fatalf("table:\n%s", out.String())
	}
	out.Reset()
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	logPath, _ := keepFiles(t, ctx)
	if !strings.Contains(out.String(), "keeper       ok     not installed; wt sync keep start; last pass Mar 4 14:20 rebased 1, pushed 1, left 0; log "+logPath+"\n") {
		t.Fatalf("doctor:\n%s", out.String())
	}

	// A problem no clean pass has cleared is the doctor's business, whatever
	// the last pass itself came to.
	_, statePath := keepFiles(t, ctx)
	st := readKeep(t, ctx)
	st.Result = "trunk unchanged"
	st.Problem, st.ProblemAt = "failed: not completed: bump (push failed)", keepDay
	if err := writeKeepState(statePath, st); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "keeper       warn   not installed; wt sync keep start; last pass Mar 4 14:20 trunk unchanged; problem since Mar 4 14:20: failed: not completed: bump (push failed); log ") {
		t.Fatalf("doctor:\n%s", out.String())
	}

	// Without launchd only the installed part changes: a pass run from cron
	// still shows in the row, the table and the status.
	onPlatform(t, "linux")
	out.Reset()
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "keeper       warn   no launchd here; run wt sync keep once from cron; last pass Mar 4 14:20 trunk unchanged; problem since Mar 4 14:20: failed: not completed: bump (push failed); log "+logPath+"\n") {
		t.Fatalf("doctor off macOS:\n%s", out.String())
	}
	out.Reset()
	if err := Sync(ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\nkept at Mar 4 14:20, problem since Mar 4 14:20 · wt sync keep status\n") {
		t.Fatalf("table off macOS:\n%s", out.String())
	}
	out.Reset()
	if err := SyncKeepStatus(ctx, time.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if want := "keeper   no launchd on this platform; run wt sync keep once from cron\nlast     Mar 4 14:20  trunk unchanged\nproblem  since Mar 4 14:20: failed: not completed: bump (push failed)\nlog      " + logPath + "\n"; out.String() != want {
		t.Fatalf("status off macOS:\n%s\nwant:\n%s", out.String(), want)
	}
}

// The plist is a pure function of the job, checked against a golden file.
func TestKeepPlistMatchesTheGoldenFile(t *testing.T) {
	got := keepPlist(keepJob{
		Label:    "se.wt.sync-keep.myrepo-0123abcd",
		Exe:      "/usr/local/bin/wt",
		MainRoot: "/Users/me/code/myrepo",
		GitDir:   "/Users/me/code/myrepo/.git",
		Every:    90 * time.Minute,
		NoPush:   true,
		Env: map[string]string{
			"PATH":          "/opt/homebrew/bin:/usr/bin:/bin",
			"HOME":          "/Users/me",
			"SSH_AUTH_SOCK": "/Users/me/Library/Group Containers/1P&co/t/agent.sock",
		},
	})
	want, err := os.ReadFile(filepath.Join("testdata", "keep.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("plist differs from testdata/keep.plist:\n%s", got)
	}
}

// fakeLaunchctl puts a launchctl on the PATH that records its arguments,
// remembers what was bootstrapped so print answers as launchd would, and
// returns the file it records the calls in.
func fakeLaunchctl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	loaded := filepath.Join(dir, "loaded")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\ncase \"$1\" in\n" +
		"  bootstrap) touch " + loaded + " ;;\n" +
		"  bootout) rm -f " + loaded + " ;;\n" +
		"  print) [ -e " + loaded + " ] || exit 1 ;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "launchctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// start writes the plist under the home's LaunchAgents with what the shell
// had, bootstraps it and refuses a second time; stop boots it out and
// removes it, and the table then says stopped.
func TestSyncKeepStartAndStopDriveLaunchd(t *testing.T) {
	ctx, _ := runFixture(t, false)
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := fakeLaunchctl(t)
	onPlatform(t, "darwin")
	env := map[string]string{"PATH": "/usr/bin:/bin", "HOME": home, "SSH_AUTH_SOCK": "/tmp/agent.sock"}
	opts := KeepStartOptions{
		Every:  time.Hour,
		Getenv: func(k string) string { return env[k] },
		Now:    func() time.Time { return keepDay },
	}
	var out bytes.Buffer
	if err := SyncKeepStart(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	plists, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "se.wt.sync-keep.*.plist"))
	if len(plists) != 1 {
		t.Fatalf("plists %v", plists)
	}
	plist := plists[0]
	s := out.String()
	for _, want := range []string{
		"installed " + plist + "\n",
		"runs wt sync keep once every 1h in " + ctx.Repo.MainRoot + "; first at 15:20\n",
		"pushes through this shell's ssh agent (SSH_AUTH_SOCK captured; 1Password may ask to approve the key)\n",
		"· wt sync keep status\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("start lacks %q:\n%s", want, s)
		}
	}
	// The plist carries the shell's ssh command verbatim: the owner's alone.
	if info, err := os.Stat(plist); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("plist mode %v, err %v; want 0600", info.Mode().Perm(), err)
	}
	body, _ := os.ReadFile(plist)
	for _, want := range []string{"<string>sync</string>", "<string>--every</string>\n\t\t<string>1h</string>", "<integer>3600</integer>", "<key>SSH_AUTH_SOCK</key>\n\t\t<string>/tmp/agent.sock</string>", "<key>WorkingDirectory</key>\n\t<string>" + ctx.Repo.MainRoot + "</string>", "<key>GIT_CONFIG_KEY_0</key>\n\t\t<string>commit.gpgsign</string>\n\t\t<key>GIT_CONFIG_VALUE_0</key>\n\t\t<string>false</string>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("plist lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "GIT_SSH_COMMAND") {
		t.Errorf("GIT_SSH_COMMAND captured though the shell had none:\n%s", body)
	}
	called, _ := os.ReadFile(calls)
	if want := "bootstrap gui/" + strconv.Itoa(os.Getuid()) + " " + plist + "\n"; string(called) != want {
		t.Fatalf("launchctl was called with %q, want %q", called, want)
	}

	out.Reset()
	err := SyncKeepStart(ctx, opts, &out)
	if err == nil || err.Error() != "a keeper is already installed here: "+plist+"; wt sync keep status" {
		t.Fatalf("second start: %v", err)
	}

	out.Reset()
	if err := SyncKeepStatus(ctx, opts.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if want := "keeper   installed, every 1h, loaded · " + plist + "\nlast     never\nnext     15:20\nlog      "; !strings.HasPrefix(out.String(), want) {
		t.Fatalf("status:\n%s", out.String())
	}
	out.Reset()
	if err := Sync(ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\nkeeper installed, first pass at ") {
		t.Fatalf("table:\n%s", out.String())
	}

	out.Reset()
	if err := SyncKeepStop(ctx, &out); err != nil {
		t.Fatalf("stop: %v\n%s", err, out.String())
	}
	label := strings.TrimSuffix(filepath.Base(plist), ".plist")
	if want := "stopped " + label + "; removed " + plist + "\n"; out.String() != want {
		t.Fatalf("stop printed %q, want %q", out.String(), want)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatal("the plist is still there")
	}
	called, _ = os.ReadFile(calls)
	if !strings.HasSuffix(string(called), "bootout gui/"+strconv.Itoa(os.Getuid())+"/"+label+"\n") {
		t.Fatalf("launchctl calls:\n%s", called)
	}
	out.Reset()
	if err := SyncKeepStatus(ctx, opts.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "keeper   stopped · wt sync keep start\n") {
		t.Fatalf("status after stop:\n%s", out.String())
	}
	if err := SyncKeepStop(ctx, &out); err == nil || err.Error() != "no keeper is installed here; wt sync keep start" {
		t.Fatalf("second stop: %v", err)
	}
}

// start names the agent launcher's key for what it is, in both forms the
// launcher uses: a push through it stops working when that agent exits.
func TestSyncKeepStartWarnsAboutAnAgentLaunchersKey(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"wrapper":  {"PATH": "/usr/bin:/bin", "GIT_SSH_COMMAND": "/Users/me/dotfiles/.config/op/op-agent-ssh", "OP_AGENT_KEYFILE": "/tmp/op_agent_ssh.abc", "OP_AGENT_SA_TOKEN": "secret"},
		"plain -i": {"PATH": "/usr/bin:/bin", "GIT_SSH_COMMAND": "ssh -F /dev/null -i /tmp/op_agent_ssh.abc -o IdentitiesOnly=yes -o IdentityAgent=none", "OP_AGENT_SA_TOKEN": "secret"},
		"keyfile":  {"PATH": "/usr/bin:/bin", "GIT_SSH_COMMAND": "ssh -i /tmp/k", "OP_AGENT_KEYFILE": "/tmp/k"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, _ := runFixture(t, false)
			t.Setenv("HOME", t.TempDir())
			fakeLaunchctl(t)
			onPlatform(t, "darwin")
			var out bytes.Buffer
			if err := SyncKeepStart(ctx, KeepStartOptions{Getenv: func(k string) string { return env[k] }}, &out); err != nil {
				t.Fatalf("err %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "an agent launcher's key: it is shredded when that agent exits") {
				t.Fatalf("start:\n%s", out.String())
			}
			job, err := keepJobFor(ctx)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(job.PlistPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "secret") {
				t.Fatalf("the token reached the plist:\n%s", body)
			}
			if _, has := env["OP_AGENT_KEYFILE"]; has != strings.Contains(string(body), "<key>OP_AGENT_KEYFILE</key>") {
				t.Fatalf("plist:\n%s", body)
			}
		})
	}
}

// A plist removed by hand leaves the job loaded until logout: status says
// so, and stop boots it out by its label.
func TestSyncKeepStopBootsOutAJobWhosePlistIsGone(t *testing.T) {
	ctx, _ := runFixture(t, false)
	t.Setenv("HOME", t.TempDir())
	calls := fakeLaunchctl(t)
	onPlatform(t, "darwin")
	var out bytes.Buffer
	if err := SyncKeepStart(ctx, KeepStartOptions{Getenv: func(string) string { return "" }}, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	job, err := keepJobFor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(job.PlistPath); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := SyncKeepStatus(ctx, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "keeper   loaded but its plist is gone · wt sync keep stop, then start\n") {
		t.Fatalf("status:\n%s", out.String())
	}
	out.Reset()
	if err := SyncKeepStop(ctx, &out); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if want := "stopped " + job.Label + "; its plist was already gone\n"; out.String() != want {
		t.Fatalf("stop printed %q, want %q", out.String(), want)
	}
	if called, _ := os.ReadFile(calls); !strings.HasSuffix(string(called), "bootout gui/"+strconv.Itoa(os.Getuid())+"/"+job.Label+"\n") {
		t.Fatalf("launchctl calls:\n%s", called)
	}
}

// A pass that failed keeps the trunk it saw out of the record, so the next
// pass tries again, and leaves a problem the status, the table and the
// doctor carry. A refused push is the case that matters: the branch is
// rebased, so the retry has nothing to rebase, and the push itself is
// offered again every pass until it goes through; only then is the problem
// cleared.
func TestSyncKeepRunRetriesARefusedPushUntilItGoesThrough(t *testing.T) {
	ctx, bump := runFixture(t, false)
	t.Setenv("HOME", t.TempDir())
	onPlatform(t, "darwin")
	pushURL := gitOut(t, ctx.Repo.MainRoot, "remote", "get-url", "--push", "origin")
	gitOut(t, ctx.Repo.MainRoot, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "nowhere"))
	opts := keepOpts(PushAlways)
	var out bytes.Buffer
	err := SyncKeepRun(ctx, opts, &out)
	if err == nil || err.Error() != "not completed: bump (push failed)" {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	command := "push: git -C " + bump + " push --force-with-lease --force-if-includes"
	if !strings.Contains(out.String(), command) {
		t.Fatalf("the failed push is not spelled out:\n%s", out.String())
	}
	// In the log too: under launchd stdout goes nowhere.
	logPath, _ := keepFiles(t, ctx)
	if logged, _ := os.ReadFile(logPath); !strings.Contains(string(logged), "    "+command) || !strings.Contains(string(logged), "      ✗ push of feat_wt/bump failed") {
		t.Fatalf("log:\n%s", logged)
	}
	tip := gitOut(t, bump, "rev-parse", "HEAD")
	st := readKeep(t, ctx)
	if st.TrunkSHA != "" || st.Problem != "failed: not completed: bump (push failed)" || !st.ProblemAt.Equal(keepDay) {
		t.Fatalf("state %+v", st)
	}
	if len(st.Unpushed) != 1 || st.Unpushed[0] != (unpushedTarget{Work: "bump", Branch: "feat_wt/bump", Path: bump, Tip: tip}) {
		t.Fatalf("unpushed %+v", st.Unpushed)
	}
	out.Reset()
	if err := SyncKeepStatus(ctx, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\nproblem  since 14:20: failed: not completed: bump (push failed)\nunpushed bump\n") {
		t.Fatalf("status:\n%s", out.String())
	}
	out.Reset()
	if err := Sync(ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\nkept at Mar 4 14:20, problem since Mar 4 14:20 · wt sync keep status\n") {
		t.Fatalf("table:\n%s", out.String())
	}
	out.Reset()
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "keeper       warn   not installed; wt sync keep start; last pass Mar 4 14:20 failed: not completed: bump (push failed); problem since Mar 4 14:20: failed: not completed: bump (push failed); unpushed: bump; log ") {
		t.Fatalf("doctor:\n%s", out.String())
	}

	// Still refused: the retry is the pass's failure, and the problem stays.
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err == nil || err.Error() != "not completed: bump (push failed)" {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "✗ push of feat_wt/bump failed") {
		t.Fatalf("the push was not retried:\n%s", out.String())
	}
	if st := readKeep(t, ctx); st.TrunkSHA != "" || st.Problem == "" || len(st.Unpushed) != 1 {
		t.Fatalf("state %+v", st)
	}

	// Origin reachable again: the next pass has nothing to rebase (bump is
	// current) and pushes it anyway, which clears the problem and records
	// trunk.
	gitOut(t, ctx.Repo.MainRoot, "remote", "set-url", "--push", "origin", pushURL)
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"nothing is ready to rebase\n", "✓ pushed feat_wt/bump", "kept: nothing to rebase, left 0, pushed 1 · log "} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "rebased 1 commit") {
		t.Fatalf("rebased again:\n%s", s)
	}
	if gitOut(t, ctx.Repo.MainRoot, "rev-parse", "refs/remotes/origin/feat_wt/bump") != tip {
		t.Fatal("bump did not reach origin")
	}
	st = readKeep(t, ctx)
	if st.TrunkSHA != gitOut(t, ctx.Repo.MainRoot, "rev-parse", "origin/main") || st.Problem != "" || !st.ProblemAt.IsZero() || len(st.Unpushed) != 0 {
		t.Fatalf("state %+v", st)
	}
	out.Reset()
	if err := SyncKeepStatus(ctx, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "problem") || strings.Contains(out.String(), "unpushed") {
		t.Fatalf("status:\n%s", out.String())
	}
}

// A branch somebody moved after the refused push is theirs: it is dropped
// from the retries without a push, and the pass ends clean.
func TestSyncKeepRunDropsAnUnpushedBranchThatMoved(t *testing.T) {
	ctx, bump := runFixture(t, false)
	pushURL := gitOut(t, ctx.Repo.MainRoot, "remote", "get-url", "--push", "origin")
	gitOut(t, ctx.Repo.MainRoot, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "nowhere"))
	opts := keepOpts(PushAlways)
	var out bytes.Buffer
	if err := SyncKeepRun(ctx, opts, &out); err == nil {
		t.Fatalf("the push went through to nowhere:\n%s", out.String())
	}
	writeFile(t, bump, "mine.txt", "mine\n")
	gitOut(t, bump, "add", "-A")
	gitOut(t, bump, "commit", "-q", "-m", "moved by hand")
	gitOut(t, ctx.Repo.MainRoot, "remote", "set-url", "--push", "origin", pushURL)
	out.Reset()
	if err := SyncKeepRun(ctx, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "pushed feat_wt/bump") {
		t.Fatalf("a branch that moved was pushed:\n%s", out.String())
	}
	if st := readKeep(t, ctx); len(st.Unpushed) != 0 || st.Problem != "" {
		t.Fatalf("state %+v", st)
	}
}

// A pass Ctrl-C or a bootout ended leaves its pid in the json: the status
// says it was interrupted rather than running, and the next pass goes ahead.
func TestSyncKeepStatusSaysWhenAPassWasInterrupted(t *testing.T) {
	ctx, _ := runFixture(t, false)
	t.Setenv("HOME", t.TempDir())
	onPlatform(t, "darwin")
	_, statePath := keepFiles(t, ctx)
	// The largest pid the kernel hands out is below this on every platform
	// wt runs on, so nothing is running under it.
	dead := 1<<31 - 1
	if err := writeKeepState(statePath, keepState{PID: dead, Started: keepDay.Add(-20 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := SyncKeepStatus(ctx, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\npass     interrupted at 14:00 (pid "+strconv.Itoa(dead)+"); the next one goes ahead\n") {
		t.Fatalf("status:\n%s", out.String())
	}
	// With the lock held a pass is running, whatever pid the json names.
	gitDir, _, _, err := keepPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lock, held, err := acquireKeepLock(gitDir)
	if err != nil || held {
		t.Fatalf("lock: %v, held %v", err, held)
	}
	defer lock.Close()
	out.Reset()
	if err := SyncKeepStatus(ctx, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\npass     running, pid "+strconv.Itoa(dead)+" since 14:00\n") {
		t.Fatalf("status:\n%s", out.String())
	}
}

// An installed keeper whose next pass is more than an interval overdue is
// said to be: launchd is not running it.
func TestSyncKeepStatusSaysWhenTheNextPassIsOverdue(t *testing.T) {
	ctx, _ := runFixture(t, false)
	t.Setenv("HOME", t.TempDir())
	fakeLaunchctl(t)
	onPlatform(t, "darwin")
	var out bytes.Buffer
	if err := SyncKeepStart(ctx, KeepStartOptions{Getenv: func(string) string { return "" }, Now: func() time.Time { return keepDay }}, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	out.Reset()
	if err := SyncKeepStatus(ctx, keepDay.Add(2*time.Hour), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\nnext     14:50 (overdue)\n") {
		t.Fatalf("status:\n%s", out.String())
	}
}

// A pass turns commit signing off for every git it runs, on top of whatever
// stack a launcher left, so a signer that asks is never asked.
func TestUnsignCommitsAppendsToTheConfigStack(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "T")
	unsignCommits()
	if os.Getenv("GIT_CONFIG_COUNT") != "2" || os.Getenv("GIT_CONFIG_KEY_1") != "commit.gpgsign" || os.Getenv("GIT_CONFIG_VALUE_1") != "false" || os.Getenv("GIT_CONFIG_KEY_0") != "user.name" {
		t.Fatalf("stack: COUNT=%s KEY_0=%s KEY_1=%s VALUE_1=%s", os.Getenv("GIT_CONFIG_COUNT"), os.Getenv("GIT_CONFIG_KEY_0"), os.Getenv("GIT_CONFIG_KEY_1"), os.Getenv("GIT_CONFIG_VALUE_1"))
	}
	t.Setenv("GIT_CONFIG_KEY_1", "")
	t.Setenv("GIT_CONFIG_VALUE_1", "")
}

// Off macOS, start and stop say so and exit 2; run and status still work.
func TestSyncKeepStartAndStopAreRefusedWithoutLaunchd(t *testing.T) {
	ctx, _ := runFixture(t, false)
	t.Setenv("HOME", t.TempDir())
	onPlatform(t, "linux")
	var out bytes.Buffer
	if err := SyncKeepStart(ctx, KeepStartOptions{}, &out); err != ErrNoLaunchd {
		t.Fatalf("start: %v", err)
	}
	if err := SyncKeepStop(ctx, &out); err != ErrNoLaunchd {
		t.Fatalf("stop: %v", err)
	}
	if err := SyncKeepStatus(ctx, time.Now(), &out); err != nil || !strings.HasPrefix(out.String(), "keeper   no launchd on this platform; run wt sync keep once from cron\n") {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
}

// Looking is not keeping: the table line and the status ask whether a pass
// is running without leaving a lock file in a repository never kept.
func TestLookingAtTheKeeperLeavesNoLockFile(t *testing.T) {
	ctx, _ := runFixture(t, false)
	t.Setenv("HOME", t.TempDir())
	onPlatform(t, "darwin")
	gitDir, _, _, err := keepPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Sync(ctx, SyncOptions{NoFetch: true}, &out); err != nil {
		t.Fatal(err)
	}
	if err := SyncKeepStatus(ctx, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(gitDir, KeepLockName)); !os.IsNotExist(err) {
		t.Fatalf("a look left the lock file behind: %v", err)
	}
	if strings.Contains(out.String(), "pass     running") {
		t.Fatalf("nothing runs, yet:\n%s", out.String())
	}
}

func TestFmtEvery(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Minute: "30m", time.Hour: "1h", 90 * time.Minute: "1h30m", 45 * time.Second: "45s", 2 * time.Hour: "2h", 70 * time.Second: "1m10s", 80 * time.Second: "1m20s", 90 * time.Second: "1m30s", time.Hour + 5*time.Second: "1h0m5s"} {
		if got := fmtEvery(d); got != want {
			t.Errorf("fmtEvery(%s) = %q, want %q", d, got, want)
		}
	}
}
