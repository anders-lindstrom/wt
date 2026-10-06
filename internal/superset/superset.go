// Package superset registers worktrees with the Superset desktop app through
// its command line.
//
// Superset has three states: not installed, installed with its host service
// stopped, and running. Only a running host answers anything but `status`.
// `status` itself can call a running host stale: that is
// WORKAROUND(superset-stale-status), at Probe. Nothing here starts the host
// service.
package superset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// The deadlines bounding the CLI. The read-only calls answer from a local
// socket; `ws create` and `ws delete` do git work and one cloud call, so they
// get longer. A Superset that has stopped answering must not hold wt open.
var (
	readDeadline     = 10 * time.Second
	registerDeadline = 30 * time.Second
)

// CLI is a located Superset command line.
type CLI struct{ Exe string }

// Find locates the Superset CLI: the PATH first, then the shim the desktop
// app installs at ~/.superset/bin/superset, which is not on the PATH.
func Find() (CLI, bool) {
	if exe, err := exec.LookPath("superset"); err == nil {
		return CLI{Exe: exe}, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return CLI{}, false
	}
	shim := filepath.Join(home, ".superset", "bin", "superset")
	if st, err := os.Stat(shim); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
		return CLI{Exe: shim}, true
	}
	return CLI{}, false
}

// Status is what Superset can do right now.
type Status struct {
	// Exe is the CLI that was found, empty when Superset is not installed.
	Exe string
	// Running says the host service answers: `status` said so, or called
	// its manifest stale and the host answered a read all the same. Nothing
	// but `status` works when it is false.
	Running bool
	// WORKAROUND(superset-stale-status): Stale is `status` calling the
	// host's manifest stale, PID the host pid it names, stale or running.
	// Stale with Running is `status` being wrong.
	Stale bool
	PID   int
	// Err is why the state could not be read, when it could not.
	Err error
}

// Installed reports whether a Superset CLI was found at all.
func (s Status) Installed() bool { return s.Exe != "" }

// CLI returns the located command line.
func (s Status) CLI() CLI { return CLI{Exe: s.Exe} }

// Probe locates the CLI and asks whether the host service is running.
// `status` is the one command a stopped host still answers, so this is the
// gate every other call goes through.
//
// WORKAROUND(superset-stale-status), written against Superset 1.36.0: its
// `status` calls a serving host stale when the pid's command line lacks
// "superset-host", which is so for the desktop app's host and for the one the
// Homebrew CLI starts. The --local commands only want the pid alive, so a
// stale answer is settled by asking the host for its projects: one more
// process, in that state only.
//
// Superset has fixed it when `superset status --json` says running: true for
// such a host, and `wt doctor` says so when it sees that (StaleStatusFixed).
// Then everything carrying the marker goes, and `running` is believed as it
// stands.
func Probe() Status {
	c, ok := Find()
	if !ok {
		return Status{}
	}
	out, err := c.run(readDeadline, "status", "--json")
	if err != nil {
		return Status{Exe: c.Exe, Err: err}
	}
	var st struct {
		Running bool `json:"running"`
		Stale   bool `json:"stale"`
		PID     int  `json:"pid"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return Status{Exe: c.Exe, Err: fmt.Errorf("superset status --json: %w", err)}
	}
	s := Status{Exe: c.Exe, Running: st.Running, Stale: st.Stale, PID: st.PID}
	// WORKAROUND(superset-stale-status)
	if !s.Running && s.Stale && s.PID > 0 {
		_, err := c.Projects()
		s.Running = err == nil
	}
	return s
}

// StaleStatusMarker is on every piece of the stale-status workaround. Probe
// says when they go.
const StaleStatusMarker = "WORKAROUND(superset-stale-status)"

// hostProcessName is what 1.36.0's `status` wants in a host pid's command
// line before it calls the host running. WORKAROUND(superset-stale-status)
const hostProcessName = "superset-host"

// StaleStatusFixed reports whether `status` called a host running that 1.36.0
// calls stale: one whose command line, as command reads it, lacks
// "superset-host". A status with no pid, or a pid command cannot read, is no
// evidence. WORKAROUND(superset-stale-status)
func (s Status) StaleStatusFixed(command func(pid int) (string, error)) bool {
	if !s.Running || s.Stale || s.PID <= 0 {
		return false
	}
	line, err := command(s.PID)
	return err == nil && line != "" && !strings.Contains(line, hostProcessName)
}

// ProcessCommand is a process's command line as `ps` gives it, which is what
// Superset's own check reads. WORKAROUND(superset-stale-status)
func ProcessCommand(pid int) (string, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	timedOut, _, err := git.RunBounded(readDeadline, cmd)
	if timedOut {
		return "", fmt.Errorf("ps -p %d did not answer within %s", pid, readDeadline)
	}
	if err != nil {
		return "", fmt.Errorf("ps -p %d: %w", pid, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Version is the CLI's own version, or "" when it will not say. Kept out of
// Probe: only `wt doctor` needs it, and folding it in costs a process on
// every worktree.
func (c CLI) Version() string {
	out, err := c.run(readDeadline, "--version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Project is one repository Superset knows about.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Path is the repository's main checkout on this machine.
	Path string `json:"path"`
}

// Projects lists the projects registered on this machine. The ids here are
// the ones `ws create` takes; the ones in ~/.superset/local.db are not.
func (c CLI) Projects() ([]Project, error) {
	out, err := c.run(readDeadline, "projects", "list", "--local", "--json")
	if err != nil {
		return nil, err
	}
	var ps []Project
	if err := json.Unmarshal(out, &ps); err != nil {
		return nil, fmt.Errorf("superset projects list --json: %w", err)
	}
	return ps, nil
}

// ProjectAt returns the project whose repository is rooted at root. Both
// sides are resolved through symlinks: a symlinked home or /tmp spells the
// same repository two ways, and the project would go missing.
func ProjectAt(ps []Project, root string) (Project, bool) {
	want := resolve(root)
	for _, p := range ps {
		if resolve(p.Path) == want {
			return p, true
		}
	}
	return Project{}, false
}

func resolve(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// Workspace is a registered workspace.
type Workspace struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Branch string `json:"branch"`
}

// Registration is what `ws create` did.
type Registration struct {
	Workspace Workspace `json:"workspace"`
	// AlreadyExists is Superset saying this branch already had a workspace,
	// so the call changed nothing. Registering twice is safe because of it.
	AlreadyExists bool `json:"alreadyExists"`
}

// Register adopts the worktree git already holds for branch as a workspace of
// project. Superset creates no checkout: it looks the branch up in `git
// worktree list` and registers the path it finds. --skip-branch-prefix stops
// it namespacing the branch under the project's own prefix.
func (c CLI) Register(projectID, branch string) (Registration, error) {
	out, err := c.run(registerDeadline,
		"ws", "create", "--local",
		"--project", projectID,
		"--name", branch,
		"--branch", branch,
		"--skip-branch-prefix",
		"--json")
	if err != nil {
		return Registration{}, err
	}
	var r Registration
	if err := json.Unmarshal(out, &r); err != nil {
		return Registration{}, fmt.Errorf("superset ws create --json: %w", err)
	}
	return r, nil
}

// ListedWorkspace is one row of `ws list --local`: a live workspace on this
// machine. Superset's archived rows are not listed.
type ListedWorkspace struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
	// Type is "worktree" for a workspace on a worktree, "local" for the
	// one on a project's main checkout.
	Type         string `json:"type"`
	WorktreePath string `json:"worktreePath"`
	ArchivedAt   any    `json:"archivedAt"`
}

// Workspaces lists the live workspaces on this machine.
func (c CLI) Workspaces() ([]ListedWorkspace, error) {
	out, err := c.run(readDeadline, "ws", "list", "--local", "--json")
	if err != nil {
		return nil, err
	}
	var ws []ListedWorkspace
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, fmt.Errorf("superset ws list --json: %w", err)
	}
	return ws, nil
}

// WorkspacesAt returns the live worktree workspaces whose checkout is path,
// compared through symlinks. A "local" workspace is a project's main
// checkout, which no removal touches; an archived row is Superset's own.
// path must still exist for its symlinks to resolve.
func WorkspacesAt(ws []ListedWorkspace, path string) []ListedWorkspace {
	want := resolve(path)
	var found []ListedWorkspace
	for _, w := range ws {
		if w.Type == "worktree" && w.ArchivedAt == nil && w.WorktreePath != "" && resolve(w.WorktreePath) == want {
			found = append(found, w)
		}
	}
	return found
}

// Delete deletes the workspace id on this machine and returns Superset's
// warnings. Superset force-removes the workspace's worktree as part of it,
// so it is only for a workspace whose checkout is already gone. The CLI
// never deletes the branch.
func (c CLI) Delete(id string) (warnings []string, err error) {
	out, err := c.run(registerDeadline, "ws", "delete", "--local", id, "--json")
	if err != nil {
		return nil, err
	}
	var d struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, fmt.Errorf("superset ws delete --json: %w", err)
	}
	return d.Warnings, nil
}

// run executes the CLI under a deadline, through the same bounded runner git
// calls use, so the deadline and an interrupt take down whatever it forked.
func (c CLI) run(deadline time.Duration, args ...string) ([]byte, error) {
	cmd := exec.Command(c.Exe, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	timedOut, _, err := git.RunBounded(deadline, cmd)
	if timedOut {
		return nil, fmt.Errorf("superset %s did not answer within %s", strings.Join(args, " "), deadline)
	}
	if err != nil {
		return nil, fmt.Errorf("superset %s failed: %s", strings.Join(args, " "), reason(stderr.String(), err))
	}
	return stdout.Bytes(), nil
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// reason is the CLI's own complaint, written as an "Error: <what>" line
// followed by a "Hint:" line. Only a superset that ran and exited has one.
func reason(stderr string, err error) string {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return err.Error()
	}
	for _, line := range strings.Split(ansi.ReplaceAllString(stderr, ""), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "│ "))
		if line == "" {
			continue
		}
		return strings.TrimPrefix(line, "Error: ")
	}
	return err.Error()
}
