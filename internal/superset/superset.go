// Package superset registers worktrees with the Superset desktop app through
// its command line.
//
// Superset has three states: not installed, installed with its host service
// stopped, and running. Only a running host answers anything but `status`.
// Nothing here starts the host service.
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
	// Running says the host service answered. Nothing but `status` works
	// when it is false.
	Running bool
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
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return Status{Exe: c.Exe, Err: fmt.Errorf("superset status --json: %w", err)}
	}
	return Status{Exe: c.Exe, Running: st.Running}
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
