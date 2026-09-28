package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
)

// unconfiguredRepo is a repository with a commit on main and no worktree
// configuration: what a clone of someone else's project is.
func unconfiguredRepo(t *testing.T) string {
	t.Helper()
	main := committedRepo(t, minimalConf)
	if err := os.RemoveAll(filepath.Join(main, "bin")); err != nil {
		t.Fatal(err)
	}
	return main
}

// A repository with no configuration runs on what `wt init --yes` would
// write, held in memory: the repository is left as it was.
func TestOpenRunsOnDetectedDefaultsWithoutAConfigFile(t *testing.T) {
	main := unconfiguredRepo(t)
	ctx, err := Open(main)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c := ctx.Config
	if !c.Detected || c.MainBranch != "main" || c.BranchPrefix != "feat_wt" || c.DefaultType != "feat" {
		t.Errorf("detected %v trunk %q prefix %q type %q", c.Detected, c.MainBranch, c.BranchPrefix, c.DefaultType)
	}
	if c.BuildInitEnabled || c.BuildInitCommand != "" {
		t.Errorf("detected defaults run a build: %v %q", c.BuildInitEnabled, c.BuildInitCommand)
	}
	if _, err := os.Stat(filepath.Join(main, "bin")); !os.IsNotExist(err) {
		t.Errorf("detection wrote to the repository: %v", err)
	}
}

// The detected configuration is exactly the one `wt init --yes` writes, bar
// the flags saying no file holds it and where its trunk came from.
func TestDetectedDefaultsAreWhatInitWrites(t *testing.T) {
	main := unconfiguredRepo(t)
	detected, err := Open(main)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := Init(detected.Repo, InitOptions{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	written, err := Open(main)
	if err != nil {
		t.Fatalf("Open after init: %v", err)
	}
	if written.Config.Detected {
		t.Error("a written configuration still reads as detected")
	}
	got, want := *detected.Config, *written.Config
	got.Detected, got.MainBranchSet, got.TrunkSource = false, want.MainBranchSet, want.TrunkSource
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detected\n  %+v\nwritten\n  %+v", got, want)
	}
}

// A trunk that is only a guess — no origin/HEAD, and the checkout's branch has
// no commit — is not detected: today's error stands, saying what could not be
// detected and what fixes it.
func TestOpenWithoutConfigOrTrunkKeepsTheError(t *testing.T) {
	r := bareRepo(t)
	_, err := Open(r.Root)
	if !errors.Is(err, config.ErrNoConfig) {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
	for _, want := range []string{"trunk", "main", "wt init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// Inside a worktree of an unconfigured repository, neither checkout has a
// file, and the detection is the main checkout's.
func TestOpenInsideAWorktreeOfAnUnconfiguredRepo(t *testing.T) {
	ctx, err := Open(unconfiguredRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	path, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in, err := Open(path)
	if err != nil {
		t.Fatalf("Open in the worktree: %v", err)
	}
	if !in.Config.Detected || in.Config.MainBranch != "main" {
		t.Errorf("detected %v trunk %q", in.Config.Detected, in.Config.MainBranch)
	}
}

// wt new on detected defaults says so once, on the warning stream, naming the
// trunk it took; the log and the path are what they always are.
func TestNewOnDetectedDefaultsSaysSoOnce(t *testing.T) {
	ctx, err := Open(unconfiguredRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	var warn, log bytes.Buffer
	ctx.WarnTo(&warn)
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &log); err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := New(ctx, "fix/other", NewOptions{}, &log); err != nil {
		t.Fatalf("New: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(warn.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "detected") ||
		!strings.Contains(lines[0], "main") || !strings.Contains(lines[0], "wt init") {
		t.Errorf("want one line naming the detected trunk and wt init, got:\n%s", warn.String())
	}
	if strings.Contains(log.String(), "detected") {
		t.Errorf("the note went to the log as well:\n%s", log.String())
	}
}

// A configured repository hears nothing about detection.
func TestNewWithAConfigFileSaysNothingAboutDetection(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	ctx.WarnTo(&warn)
	if _, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &bytes.Buffer{}); err != nil {
		t.Fatalf("New: %v", err)
	}
	if warn.Len() != 0 {
		t.Errorf("a configured repository was warned:\n%s", warn.String())
	}
}

// The Claude Code hooks are what an unconfigured repository most needs to
// work in: the harness makes its worktree there and cannot run `wt init`.
func TestClaudeHooksInAnUnconfiguredRepo(t *testing.T) {
	ctx, err := Open(unconfiguredRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	var out, log bytes.Buffer
	if err := HookCreate(ctx, strings.NewReader(`{"name":"fix/login-crash"}`), &out, &log); err != nil {
		t.Fatalf("HookCreate: %v\n%s", err, log.String())
	}
	path := strings.TrimSuffix(out.String(), "\n")
	if strings.Contains(path, "\n") || !filepath.IsAbs(path) {
		t.Fatalf("stdout is not one path: %q", out.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	if err := HookRemove(ctx, strings.NewReader(`{"path":"`+path+`"}`), &log); err != nil {
		t.Fatalf("HookRemove: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
}

// wt config names where its values came from when no file did.
func TestConfigSaysTheValuesAreDetected(t *testing.T) {
	ctx, err := Open(unconfiguredRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, false, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "detected (no config file; `wt init` writes one)") {
		t.Errorf("wt config does not say its values are detected:\n%s", buf.String())
	}

	ctx, _ = Open(committedRepo(t, minimalConf))
	buf.Reset()
	if err := Config(ctx, false, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "detected") {
		t.Errorf("a configured repository reads as detected:\n%s", buf.String())
	}
}

// wt doctor reports detection as information, not as a problem.
func TestDoctorReportsDetectedDefaultsAsInformation(t *testing.T) {
	ctx := OpenLenient(unconfiguredRepo(t), &bytes.Buffer{})
	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if problems != 0 {
		t.Errorf("%d problem(s) on detected defaults:\n%s", problems, out)
	}
	if !strings.Contains(out, "  - no config file") || !strings.Contains(out, "wt init") {
		t.Errorf("doctor does not say the values are detected:\n%s", out)
	}
}

// configured:false with no problem is a plan on detected defaults.
func TestPlansOnDetectedDefaults(t *testing.T) {
	ctx := OpenLenient(unconfiguredRepo(t), &bytes.Buffer{})
	p := newPlanOf(t, ctx, "fix/x", NewOptions{})
	if p.Configured || len(p.Problems) != 0 || p.Token == nil || p.BuildCommand != nil {
		t.Errorf("configured %v problems %s token %v build %v", p.Configured, codes(p.Problems), p.Token, p.BuildCommand)
	}
	gitIn(t, ctx.Repo.MainRoot, "branch", "release-2.1")
	c := checkoutPlanOf(t, ctx, "release-2.1", "")
	if c.Configured || len(c.Problems) != 0 {
		t.Errorf("checkout: configured %v problems %s", c.Configured, codes(c.Problems))
	}
	path, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	s := planOfWork(t, ctx, path)
	if s.Configured || !s.UpEligible {
		t.Errorf("status: configured %v eligible %v (%v)", s.Configured, s.UpEligible, s.UpIneligibleReason)
	}
}

// noConfiguration is left for a repository whose trunk could not be detected:
// no origin, a detached checkout, and no branch with a conventional name.
func TestPlanWhenDetectionFails(t *testing.T) {
	main := unconfiguredRepo(t)
	gitIn(t, main, "checkout", "-q", "--detach")
	gitIn(t, main, "branch", "-m", "main", "release")
	ctx := OpenLenient(main, &bytes.Buffer{})
	p := newPlanOf(t, ctx, "fix/x", NewOptions{Base: "release"})
	if p.Configured || codes(p.Problems) != "noConfiguration" ||
		!strings.Contains(p.Problems[0].Message, "trunk") {
		t.Errorf("configured %v problems %+v", p.Configured, p.Problems)
	}
}
