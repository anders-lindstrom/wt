package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// keepFleet is three repositories under one profile: two that declare wt
// sync, one of them already kept every hour, and one that does not. The
// home and launchctl are the test's own.
func keepFleet(t *testing.T) (u *config.User, kept, fresh, undeclared *Context, home, calls string) {
	t.Helper()
	kept, _ = runFixture(t, false)
	fresh, _ = runFixture(t, false)
	plain := committedRepo(t, minimalConf)
	gitIn(t, plain, "remote", "add", "origin", plain)
	gitIn(t, plain, "fetch", "-q", "origin")
	undeclared, err := Open(plain)
	if err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	calls = fakeLaunchctl(t)
	onPlatform(t, "darwin")
	var out bytes.Buffer
	if err := SyncKeepStart(kept, keepStartOpts(time.Hour), &out); err != nil {
		t.Fatalf("start: %v\n%s", err, out.String())
	}
	u = &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{kept.Repo.MainRoot, fresh.Repo.MainRoot, plain}}}}
	return u, kept, fresh, undeclared, home, calls
}

func keepStartOpts(every time.Duration) KeepStartOptions {
	return KeepStartOptions{
		Every:  every,
		Getenv: func(k string) string { return map[string]string{"PATH": "/usr/bin:/bin"}[k] },
		Now:    func() time.Time { return keepDay },
	}
}

func plistsIn(t *testing.T, home string) []string {
	t.Helper()
	plists, err := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "se.wt.sync-keep.*.plist"))
	if err != nil {
		t.Fatal(err)
	}
	return plists
}

// The row a command across repositories printed for a repository.
func keepRowFor(t *testing.T, out string, ctx *Context) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 1 && (f[0] == abbreviateHome(ctx.Repo.MainRoot, home) || f[1] == abbreviateHome(ctx.Repo.MainRoot, home)) {
			return l
		}
	}
	t.Fatalf("no row for %s:\n%s", ctx.Repo.MainRoot, out)
	return ""
}

// start across repositories installs a job where one is missing, leaves an
// existing one as it is, skips a repository that has not opted into wt
// sync, asks once, and a second start has nothing to do.
func TestSyncKeepStartAllInstallsWhatIsMissing(t *testing.T) {
	u, kept, fresh, undeclared, home, _ := keepFleet(t)
	keptJob, err := keepJobFor(kept)
	if err != nil {
		t.Fatal(err)
	}
	keptBody, _ := os.ReadFile(keptJob.PlistPath)
	var asked []string
	opts := KeepStartAllOptions{KeepStartOptions: keepStartOpts(0),
		Ask: func(q string) (bool, error) { asked = append(asked, q); return true, nil }}
	var out bytes.Buffer
	if err := SyncKeepStartAll(u, Selection{Profiles: []string{"p"}}, opts, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	s := out.String()
	if !slices.Equal(asked, []string{"Install a keeper in 1 repository?"}) {
		t.Errorf("asked %q", asked)
	}
	for ctx, want := range map[*Context]string{
		kept:       "already kept, every 1h; left as it is",
		undeclared: "skipped: no .wt-sync.yaml on origin/main",
	} {
		if row := keepRowFor(t, s, ctx); !strings.HasSuffix(row, want) {
			t.Errorf("row %q lacks %q", row, want)
		}
	}
	if !strings.Contains(s, "  installed, every 30m, first at 14:50\n") || !strings.Contains(s, "pushes with no ssh agent") {
		t.Errorf("output:\n%s", s)
	}
	if plists := plistsIn(t, home); len(plists) != 2 {
		t.Fatalf("plists %v", plists)
	}
	if body, _ := os.ReadFile(keptJob.PlistPath); !bytes.Equal(body, keptBody) {
		t.Fatal("the existing job's plist was rewritten")
	}
	freshJob, _ := keepJobFor(fresh)
	if body, err := os.ReadFile(freshJob.PlistPath); err != nil || !strings.Contains(string(body), "<integer>1800</integer>") {
		t.Fatalf("fresh plist: %v\n%s", err, body)
	}
	if st := readKeep(t, fresh); st.Job != freshJob.Label || st.Interval != "30m" {
		t.Fatalf("fresh state %+v", st)
	}

	out.Reset()
	asked = nil
	if err := SyncKeepStartAll(u, Selection{Profiles: []string{"p"}}, opts, &out); err != nil {
		t.Fatalf("second start: %v\n%s", err, out.String())
	}
	if len(asked) != 0 || !strings.HasSuffix(out.String(), "Nothing to install.\n") {
		t.Fatalf("second start asked %q:\n%s", asked, out.String())
	}
	if plists := plistsIn(t, home); len(plists) != 2 {
		t.Fatalf("plists after the second start %v", plists)
	}
}

// --dry-run, a no, and no terminal without --yes all print the plan and
// install nothing; launchd is not touched.
func TestSyncKeepStartAllInstallsNothingUnlessAgreed(t *testing.T) {
	for name, c := range map[string]struct {
		opts KeepStartAllOptions
		last string
	}{
		"dry-run":     {KeepStartAllOptions{DryRun: true}, "Nothing was installed: --dry-run.\n"},
		"declined":    {KeepStartAllOptions{Ask: func(string) (bool, error) { return false, nil }}, "Nothing was installed.\n"},
		"no terminal": {KeepStartAllOptions{}, "Nothing was installed: there is no terminal to ask. Pass --yes to install a keeper in 1 repository.\n"},
	} {
		t.Run(name, func(t *testing.T) {
			u, _, fresh, _, home, calls := keepFleet(t)
			before, _ := os.ReadFile(calls)
			c.opts.KeepStartOptions = keepStartOpts(0)
			var out bytes.Buffer
			if err := SyncKeepStartAll(u, Selection{Profiles: []string{"p"}}, c.opts, &out); err != nil {
				t.Fatalf("err %v\n%s", err, out.String())
			}
			if row := keepRowFor(t, out.String(), fresh); !strings.HasSuffix(row, "to install, every 30m") {
				t.Errorf("row %q", row)
			}
			if !strings.HasSuffix(out.String(), c.last) {
				t.Errorf("output:\n%s", out.String())
			}
			if plists := plistsIn(t, home); len(plists) != 1 {
				t.Fatalf("plists %v", plists)
			}
			if after, _ := os.ReadFile(calls); strings.Contains(strings.TrimPrefix(string(after), string(before)), "bootstrap") {
				t.Fatalf("launchctl bootstrapped:\n%s", after)
			}
		})
	}
}

// stop across repositories stops every job there is, lists a repository
// with none, and --dry-run stops nothing; status says it one line each.
func TestSyncKeepStopAndStatusAll(t *testing.T) {
	u, kept, fresh, undeclared, home, _ := keepFleet(t)
	sel := Selection{Profiles: []string{"p"}}
	var out bytes.Buffer
	if err := SyncKeepStatusAll(u, sel, keepDay, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	s := out.String()
	for ctx, want := range map[*Context]string{
		kept:       "installed, every 1h; log ",
		fresh:      "not installed; wt sync keep start",
		undeclared: "not kept: no .wt-sync.yaml on origin/main",
	} {
		if row := keepRowFor(t, s, ctx); !strings.Contains(row, want) {
			t.Errorf("status row %q lacks %q", row, want)
		}
	}

	out.Reset()
	if err := SyncKeepStopAll(u, sel, true, &out); err != nil {
		t.Fatalf("dry stop: %v\n%s", err, out.String())
	}
	if row := keepRowFor(t, out.String(), kept); !strings.HasSuffix(row, "to stop") || !strings.HasSuffix(out.String(), "Nothing was stopped: --dry-run.\n") {
		t.Fatalf("dry stop:\n%s", out.String())
	}
	if len(plistsIn(t, home)) != 1 {
		t.Fatal("a dry stop removed the plist")
	}

	out.Reset()
	if err := SyncKeepStopAll(u, sel, false, &out); err != nil {
		t.Fatalf("stop: %v\n%s", err, out.String())
	}
	s = out.String()
	job, _ := keepJobFor(kept)
	if row := keepRowFor(t, s, kept); !strings.HasSuffix(row, "stopped; removed "+job.PlistPath) {
		t.Errorf("row %q", row)
	}
	for _, ctx := range []*Context{fresh, undeclared} {
		if row := keepRowFor(t, s, ctx); !strings.HasSuffix(row, "no keeper") {
			t.Errorf("row %q", row)
		}
	}
	if !strings.HasSuffix(s, "Stopped a keeper in 1 repository; the logs stay.\n") {
		t.Errorf("stop:\n%s", s)
	}
	if len(plistsIn(t, home)) != 0 || launchctl.loaded(job.Label) {
		t.Fatal("the job is still there")
	}
	out.Reset()
	if err := SyncKeepStatusAll(u, sel, keepDay, &out); err != nil {
		t.Fatal(err)
	}
	if row := keepRowFor(t, out.String(), kept); !strings.Contains(row, "stopped") {
		t.Errorf("status after stop %q", row)
	}
}

// A repository that is not one any more fails the command, after the rest
// are done.
func TestSyncKeepStartAllReportsARepositoryItCannotOpen(t *testing.T) {
	u, _, fresh, _, _, _ := keepFleet(t)
	gone := filepath.Join(t.TempDir(), "gone")
	u.Profiles[0].Repos = append(u.Profiles[0].Repos, gone)
	opts := KeepStartAllOptions{KeepStartOptions: keepStartOpts(0), Yes: true}
	var out bytes.Buffer
	err := SyncKeepStartAll(u, Selection{Profiles: []string{"p"}}, opts, &out)
	if err == nil || err.Error() != "not completed: gone" {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if row := keepRowFor(t, out.String(), &Context{Repo: &repo.Repo{MainRoot: gone}}); !strings.HasPrefix(row, "! ") || !strings.HasSuffix(row, "  not there any more") {
		t.Errorf("output:\n%s", out.String())
	}
	if _, err := os.Stat(func() string { j, _ := keepJobFor(fresh); return j.PlistPath }()); err != nil {
		t.Fatal("the rest were not installed")
	}
}

// Off macOS, start and stop across repositories say so before anything.
func TestSyncKeepStartAndStopAllAreRefusedWithoutLaunchd(t *testing.T) {
	onPlatform(t, "linux")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{t.TempDir()}}}}
	var out bytes.Buffer
	if err := SyncKeepStartAll(u, Selection{Profiles: []string{"p"}}, KeepStartAllOptions{Yes: true}, &out); err != ErrNoLaunchd {
		t.Fatalf("start: %v", err)
	}
	if err := SyncKeepStopAll(u, Selection{Profiles: []string{"p"}}, false, &out); err != ErrNoLaunchd {
		t.Fatalf("stop: %v", err)
	}
}
