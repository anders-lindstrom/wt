package kit

import (
	"fmt"
	"io"
	"os"

	"github.com/anlindstrom/wt/internal/config"
	"github.com/anlindstrom/wt/internal/repo"
	"github.com/anlindstrom/wt/internal/timemachine"
)

// FindTmutil is how wt finds tmutil. A variable so a test binary's TestMain
// can put "no tmutil here" in front of all its tests; a test that wants one
// puts a fake first on the PATH and the real lookup back
// (testkit.FakeTmutil).
var FindTmutil = timemachine.Find

// WarnTimeMachine is the warning topic of a tmutil that failed.
const WarnTimeMachine = "timemachine"

// TimeMachineState is which of the four states this machine is in.
type TimeMachineState int

const (
	// TimeMachineOn is macOS, switched on, with tmutil on the PATH.
	TimeMachineOn TimeMachineState = iota
	// TimeMachineOff is the person's timemachine setting off, or a user
	// file wt could not read.
	TimeMachineOff
	// TimeMachineNotMac is any other platform: there is no Time Machine.
	TimeMachineNotMac
	// TimeMachineNoTmutil is macOS, switched on, and no tmutil on the PATH.
	TimeMachineNoTmutil
)

// TimeMachine is the state this context is in, and the tmutil to call in
// the one state that calls it.
func (c *Context) TimeMachine() (TimeMachineState, timemachine.Tmutil) {
	goos := c.Machine.Platform()
	switch {
	case goos != "darwin":
		return TimeMachineNotMac, timemachine.Tmutil{}
	case !c.UserConfig().TimeMachineOn(goos):
		return TimeMachineOff, timemachine.Tmutil{}
	}
	tm, ok := FindTmutil()
	if !ok {
		return TimeMachineNoTmutil, timemachine.Tmutil{}
	}
	return TimeMachineOn, tm
}

// TimeMachineFolder is the folder an exclusion for the worktree at path goes
// on: <repo>_wt/ when path is inside it, so one sticky exclusion covers
// every worktree made there later, and the worktree's own folder otherwise.
// It is "" for the main checkout, its contents and any folder holding it.
func (c *Context) TimeMachineFolder(path string) string {
	dir := path
	if container := c.Scheme().Container(); repo.Inside(container, path, true) {
		dir = container
	}
	if repo.Inside(dir, c.Repo.MainRoot, true) || repo.Inside(c.Repo.MainRoot, dir, true) {
		return ""
	}
	return dir
}

// ExcludeFromTimeMachine excludes the folder holding a worktree wt has just
// made, moved or adopted from Time Machine, with tmutil's sticky exclusion.
//
// It never fails the command. Switched off, off macOS, or with no tmutil on
// the PATH it runs nothing and says nothing; a folder already excluded is
// left as it is, in silence. A folder it excludes is one line on w. A tmutil
// that fails is one warning line, on the context's warning stream (stderr),
// so it is said whatever w is.
func ExcludeFromTimeMachine(c *Context, path string, w io.Writer) {
	if c.Warned[WarnTimeMachine] {
		return
	}
	state, tm := c.TimeMachine()
	if state != TimeMachineOn {
		return
	}
	dir := c.TimeMachineFolder(path)
	if dir == "" {
		return
	}
	if _, err := os.Stat(dir); err != nil {
		return
	}
	if excluded, err := tm.Excluded(dir); err != nil {
		c.Warnf(WarnTimeMachine, "%s is not excluded from Time Machine: %v", dir, err)
		return
	} else if excluded {
		return
	}
	if err := tm.Exclude(dir); err != nil {
		c.Warnf(WarnTimeMachine, "%s is not excluded from Time Machine: %v", dir, err)
		return
	}
	fmt.Fprintf(w, "✓ excluded %s from Time Machine\n", dir)
}

// DoctorTimeMachine reports which state the machine is in and, where wt
// would exclude, whether this repository's worktree folder is excluded. Only
// a tmutil missing where the person's own file switched it on is counted as
// a problem; a folder not yet excluded is excluded by the next worktree made
// in it.
func DoctorTimeMachine(c *Context, w io.Writer, report func(string, ...any)) {
	fmt.Fprintln(w, "Time Machine:")
	state, tm := c.TimeMachine()
	switch state {
	case TimeMachineNotMac:
		fmt.Fprintln(w, "  - not macOS; wt runs no tmutil here")
		return
	case TimeMachineOff:
		fmt.Fprintf(w, "  - off in your wt config; turn it on with `wt config set %s true`\n",
			config.UserKeyTimeMachine)
		fmt.Fprintln(w, "  - folders already excluded stay excluded; to back them up again, run `tmutil removeexclusion <folder>`")
		return
	case TimeMachineNoTmutil:
		const why = "no tmutil on the PATH; worktree folders are not excluded from Time Machine"
		if c.UserConfig().IsSet(config.UserKeyTimeMachine) {
			report("%s", why)
		} else {
			fmt.Fprintf(w, "  - %s\n", why)
		}
		return
	}
	fmt.Fprintf(w, "  ✓ %s — worktree folders wt makes are excluded\n", tm.Exe)
	dir := c.Scheme().Container()
	if _, err := os.Stat(dir); err != nil {
		return
	}
	switch excluded, err := tm.Excluded(dir); {
	case err != nil:
		fmt.Fprintf(w, "  - could not ask tmutil about %s: %v\n", dir, err)
	case excluded:
		fmt.Fprintf(w, "  ✓ %s is excluded\n", dir)
	default:
		fmt.Fprintf(w, "  - %s is not excluded; the next worktree wt makes here excludes it, or run `tmutil addexclusion %s`\n",
			dir, ShellQuote(dir))
	}
}
