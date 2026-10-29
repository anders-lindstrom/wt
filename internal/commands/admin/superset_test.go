package admin

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// Doctor's Superset section: what state it found, and whether that state is a
// problem — which only SUPERSET_REGISTER=on makes it.
func TestDoctorReportsSupersetState(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		mode        config.SupersetMode
		status      superset.Status
		want        string
		wantProblem bool
	}{
		"absent under auto": {
			config.SupersetAuto, superset.Status{},
			"  - no superset on the PATH or at ~/.superset/bin", false,
		},
		"absent under on": {
			config.SupersetOn, superset.Status{},
			"  ! no superset on the PATH or at ~/.superset/bin", true,
		},
		"off": {
			config.SupersetOff, superset.Status{Exe: "/nope/superset", Running: true},
			"  - SUPERSET_REGISTER=off", false,
		},
		"stopped": {
			config.SupersetAuto, superset.Status{Exe: "/nope/superset"},
			"  - /nope/superset (version unknown) is installed but its host service is not running; start the Superset app", false,
		},
		// WORKAROUND(superset-stale-status): stale and nothing answered is a
		// stopped host, in the same words.
		"stale and silent": {
			config.SupersetOn, superset.Status{Exe: "/nope/superset", Stale: true, PID: 96249},
			"  ! /nope/superset (version unknown) is installed but its host service is not running; start the Superset app", true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			testkit.OptIn(ctx)
			ctx.Config.SupersetRegister = tc.mode
			provisiontest.Stub(ctx, tc.status)

			var out bytes.Buffer
			problems, err := Doctor(ctx, &out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("doctor said\n%s\nwant a line %q", out.String(), tc.want)
			}
			if (problems > 0) != tc.wantProblem {
				t.Errorf("problems = %d, want problem = %v", problems, tc.wantProblem)
			}
		})
	}
}

// The full report, when Superset is running and knows this repository.
func TestDoctorReportsAUsableSuperset(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	dir := t.TempDir()
	exe := filepath.Join(dir, "superset")
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"  '--version ') echo 1.29.0 ;;\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + ctx.Repo.MainRoot + "\"}]' ;;\n" +
		"esac\n"
	gittest.WriteExecutable(t, exe, script)
	provisiontest.Stub(ctx, superset.Status{Exe: exe, Running: true})

	var out bytes.Buffer
	problems, err := Doctor(ctx, &out)
	if err != nil {
		t.Fatal(err)
	}
	if problems != 0 {
		t.Errorf("problems = %d, want 0:\n%s", problems, out.String())
	}
	for _, want := range []string{"(1.29.0), host service running", `✓ project "demo"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor said\n%s\nwant %q", out.String(), want)
		}
	}
}

// Doctor says the host is running and that the stale manifest is Superset's
// own report, in one line, and never tells the person to start an app that is
// already up. It is no problem even for a repository that asked for Superset.
func TestDoctorSaysStatusIsWrongAboutARunningHost(t *testing.T) {
	// Not parallel: swaps supersetws.Probe; sets environment variables for the whole test binary.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	ctx.Config.SupersetRegister = config.SupersetOn
	provisiontest.StaleSuperset(t, ctx.Repo.MainRoot)

	var out bytes.Buffer
	problems, err := Doctor(ctx, &out)
	if err != nil {
		t.Fatal(err)
	}
	if problems != 0 {
		t.Errorf("problems = %d, want 0:\n%s", problems, out.String())
	}
	for _, want := range []string{
		"(1.36.0), host service running; `superset status` reports a stale manifest for pid 96249, which is Superset's bug — nothing to start\n",
		`✓ project "demo"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor said\n%s\nwant %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "start the Superset app") {
		t.Errorf("doctor tells the person to start a running app:\n%s", out.String())
	}
}

// Doctor notices by itself when Superset has fixed its status: it calls a host
// running whose command line 1.36.0 would have refused. That is one line and
// never a problem. A host `status` always vouched for, one it still calls
// stale, one that is stopped and a pid that cannot be read say nothing.
// WORKAROUND(superset-stale-status)
func TestDoctorSaysWhenTheStaleWorkaroundCanGo(t *testing.T) {
	// Not parallel: swaps supersetws.Probe, supersetws.HostCommand; sets environment variables for the whole test binary.
	const (
		running = `{"running":true,"healthy":true,"pid":4242,"port":51234,"endpoint":"http://127.0.0.1:51234","uptimeSec":60}`
		desktop = "/Applications/Superset.app/Contents/MacOS/Superset /Applications/Superset.app/Contents/Resources/app.asar/dist/main/host-service.js"
		note    = "  - `superset status` now reports this host correctly (running, pid 4242, not a \"superset-host\" process); wt's stale-status workaround can be removed: grep wt's source for WORKAROUND(superset-stale-status)\n"
		plain   = "(1.36.0), host service running\n"
	)
	for name, tc := range map[string]struct {
		status       string
		command      string
		commandErr   error
		wantNote     bool
		wantLine     string
		wantProblems int
		wantAsked    int
	}{
		"running, the desktop app's host": {status: running, command: desktop, wantNote: true, wantLine: plain, wantAsked: 1},
		"running, a superset-host":        {status: running, command: "/usr/local/bin/superset-host", wantLine: plain, wantAsked: 1},
		"running, the pid cannot be read": {status: running, commandErr: errors.New("exit status 1"), wantLine: plain, wantAsked: 1},
		"running, no pid":                 {status: `{"running":true}`, command: desktop, wantLine: plain},
		"stale and answering": {
			status: `{"running":false,"stale":true,"pid":4242}`, command: desktop,
			wantLine: "host service running; `superset status` reports a stale manifest for pid 4242, which is Superset's bug — nothing to start\n",
		},
		"not running": {
			status: `{"running":false}`, command: desktop,
			wantLine: "its host service is not running; start the Superset app\n", wantProblems: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			testkit.OptIn(ctx)
			ctx.Config.SupersetRegister = config.SupersetOn
			provisiontest.SupersetSaying(t, ctx.Repo.MainRoot, tc.status)
			asked := provisiontest.HostCommand(t, tc.command, tc.commandErr)

			var out bytes.Buffer
			problems, err := Doctor(ctx, &out)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(out.String(), note); got != tc.wantNote {
				t.Errorf("the note is there = %v, want %v:\n%s", got, tc.wantNote, out.String())
			}
			if !tc.wantNote && strings.Contains(out.String(), "workaround") {
				t.Errorf("doctor speaks of the workaround:\n%s", out.String())
			}
			if !strings.Contains(out.String(), tc.wantLine) {
				t.Errorf("doctor said\n%s\nwant %q", out.String(), tc.wantLine)
			}
			if problems != tc.wantProblems {
				t.Errorf("problems = %d, want %d:\n%s", problems, tc.wantProblems, out.String())
			}
			if len(*asked) != tc.wantAsked {
				t.Errorf("ps was asked about %v, want %d question(s)", *asked, tc.wantAsked)
			}
		})
	}
}

// `wt doctor` names the state whatever the mode: it is where you go to read
// why nothing is being registered.
func TestDoctorAlwaysNamesAMissingSupersetProject(t *testing.T) {
	t.Parallel()
	for name, mode := range map[string]config.SupersetMode{
		"auto": config.SupersetAuto,
		"on":   config.SupersetOn,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			testkit.OptIn(ctx)
			ctx.Config.SupersetRegister = mode
			provisiontest.SupersetWithoutProject(t, ctx)

			var out bytes.Buffer
			problems, err := Doctor(ctx, &out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "no Superset project for "+ctx.Repo.MainRoot) {
				t.Errorf("doctor said\n%s", out.String())
			}
			if want := mode == config.SupersetOn; (problems > 0) != want {
				t.Errorf("problems = %d, want problem = %v", problems, want)
			}
		})
	}
}

// `wt pr checkout` is the third creating command, and goes through the same
// tail: its worktree is registered too.
func TestPRCheckoutRegistersWithSuperset(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	testkit.FakeGitHub(t, ctx, testkit.OpenPR(12, "residential_fixes", "Residents keep their doors"))
	log := provisiontest.FakeSuperset(t, ctx, ctx.Repo.MainRoot, true)

	var errs bytes.Buffer
	if _, err := PRCheckout(ctx, 12, PROptions{}, &errs); err != nil {
		t.Fatalf("PRCheckout: %v", err)
	}
	got := testkit.ArgvOf(t, log)
	if len(got) != 2 || !strings.Contains(got[1], "--branch residential_fixes") {
		t.Errorf("argv = %q, want the pull request's own branch registered", got)
	}
}
