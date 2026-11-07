package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/timemachine"
)

// NoTmutil is a tmutil lookup answering "no tmutil here": kit.FindTmutil in
// every command package's tests, so none of them runs the real one.
func NoTmutil() (timemachine.Tmutil, bool) { return timemachine.Tmutil{}, false }

// Tmutil is a fake tmutil, first on the PATH for one serial test: it keeps
// the folders it has excluded in a file and logs every call, and never
// touches an extended attribute.
type Tmutil struct {
	log, state string
}

// FakeTmutil puts a fake tmutil first on the PATH and the real lookup in
// front of it for this test, and makes ctx a Mac. failing makes every
// addexclusion fail with tmutil's own complaint on stderr.
func FakeTmutil(t *testing.T, ctx *kit.Context, failing bool) *Tmutil {
	t.Helper()
	dir := t.TempDir()
	f := &Tmutil{log: filepath.Join(dir, "calls"), state: filepath.Join(dir, "excluded")}
	add := `echo "$2" >> '` + f.state + `'`
	if failing {
		add = "echo 'Error (100002): Error setting exclusion.' >&2; exit 1"
	}
	gittest.WriteExecutable(t, filepath.Join(dir, "tmutil"), "#!/bin/sh\n"+
		`echo "$*" >> '`+f.log+"'\n"+
		"case \"$1\" in\n"+
		`  isexcluded) if grep -qxF "$2" '`+f.state+`' 2>/dev/null; then echo "[Excluded]    $2"; else echo "[Included]    $2"; fi ;;`+"\n"+
		"  addexclusion) "+add+" ;;\n"+
		"  *) exit 2 ;;\n"+
		"esac\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := kit.FindTmutil
	kit.FindTmutil = timemachine.Find
	t.Cleanup(func() { kit.FindTmutil = old })
	OnMac(ctx)
	return f
}

// OnMac is ctx on macOS, whatever the machine running the test is.
func OnMac(ctx *kit.Context) {
	if ctx != nil {
		ctx.Machine.GOOS = "darwin"
	}
}

// Calls is every tmutil invocation, one argv per line.
func (f *Tmutil) Calls(t *testing.T) []string { return f.lines(t, f.log) }

// Excluded is every folder the fake has excluded, in order.
func (f *Tmutil) Excluded(t *testing.T) []string { return f.lines(t, f.state) }

// AlreadyExcluded marks dir as excluded before the test runs anything.
func (f *Tmutil) AlreadyExcluded(t *testing.T, dir string) {
	t.Helper()
	file, err := os.OpenFile(f.state, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(dir + "\n"); err != nil {
		t.Fatal(err)
	}
}

func (f *Tmutil) lines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// TimeMachineSetting gives ctx a user file saying timemachine = value, in an
// XDG_CONFIG_HOME of this test's own.
func TimeMachineSetting(t *testing.T, ctx *kit.Context, value string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, _, err := config.SetUser(config.UserKeyTimeMachine, value); err != nil {
		t.Fatal(err)
	}
	u, err := config.LoadUser()
	if err != nil {
		t.Fatal(err)
	}
	ctx.User, ctx.UserError = u, nil
}
