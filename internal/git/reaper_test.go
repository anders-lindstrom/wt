package git

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Only a command that can write or run long is worth a reaper: every read
// wt list and wt status make, and the helpers they ask, are not.
func TestWritesTellsReadsFromWrites(t *testing.T) {
	for _, args := range [][]string{
		{"git", "rev-parse", "--verify", "HEAD"},
		{"git", "-C", "/x", "for-each-ref", "--format=%(refname)"},
		{"git", "--no-optional-locks", "status", "--porcelain"},
		{"git", "--git-dir=/x/.git", "for-each-ref"},
		{"git", "worktree", "list", "--porcelain", "-z"},
		{"git", "config", "--get-regexp", `^remote\..*\.(url|gh-resolved)$`},
		{"git", "config", "--get", "checkout.defaultRemote"},
		{"git", "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"},
		{"git", "remote"},
		{"git", "remote", "get-url", "origin"},
		{"git", "rev-list", "--count", "a..b"},
		{"git", "cat-file", "-e", "abc"},
		{"git", "ls-files", "-z"},
		{"git", "log", "--format=%H"},
		{"git", "cherry", "main", "topic"},
		{"git", "merge-base", "--is-ancestor", "a", "b"},
		{"git", "branch", "--show-current"},
		{"git", "reflog", "show", "topic"},
		{"/opt/bin/gh", "pr", "list", "--state", "open"},
		{"gh", "auth", "status", "--hostname", "github.com"},
		{"/usr/local/bin/claude", "agents", "--json"},
		{"ps", "-A", "-o", "pid="},
		{"lsof", "-a", "-d", "cwd", "-Fpn", "-p", "1"},
		{"docker", "info"},
		// What wt status simulates a rebase with: objects, never a ref or
		// a worktree.
		{"git", "merge-tree", "--write-tree", "-z", "a", "b"},
		{"git", "commit-tree", "abc", "-p", "def", "-m", "sim"},
		{"git", "hash-object", "-w", "--stdin"},
		{"git", "write-tree"},
	} {
		if Writes(args, nil) {
			t.Errorf("%q counts as writing", args)
		}
	}
	for _, args := range [][]string{
		{"git", "worktree", "add", "/x", "topic"},
		{"git", "update-ref", "refs/heads/x", "abc", ""},
		{"git", "branch", "--set-upstream-to=origin/x", "x"},
		{"git", "branch", "--unset-upstream"},
		{"git", "config", "user.name", "T"},
		{"git", "symbolic-ref", "HEAD", "refs/heads/x"},
		{"git", "rebase", "main"},
		{"git", "merge", "--ff-only", "x"},
		{"git", "fetch", "origin"},
		{"git", "submodule", "update", "--init"},
		{"git", "remote", "add", "origin", "u"},
		{"git", "reflog", "expire", "--all"},
		{"git", "-c", "alias.x=!rm -rf y", "x"},
		{"gh", "pr", "checkout", "12"},
		{"sh", "-c", "make build"},
		{"/repo/bin/worktree/provision.sh"},
		{"tar", "-x", "-f", "a.tar"},
		{"superset", "workspaces", "delete"},
		{"git", "read-tree", "abc"},
	} {
		if !Writes(args, nil) {
			t.Errorf("%q counts as read-only", args)
		}
	}
	// read-tree into a temporary index is a simulation.
	if Writes([]string{"git", "read-tree", "abc"}, []string{"GIT_INDEX_FILE=/tmp/sim/index"}) {
		t.Error("read-tree into a temporary index counts as writing")
	}
}

// When its parent is gone the reaper still reads what wt wrote before it
// went: a group wt took off the list is not killed, and it returns within
// a second although the pipe never closes.
func TestReapReadsWhatWtWroteBeforeActingOnAChangedParent(t *testing.T) {
	done := startGroup(t)
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	go func() {
		// The removal arrives after the reaper has seen its parent gone,
		// as a backlog it has not read yet would.
		fmt.Fprintf(w, "+%d\n", done)
		time.Sleep(400 * time.Millisecond)
		fmt.Fprintf(w, "-%d\n", done)
	}()
	returned := make(chan struct{})
	go func() {
		reap(r, -1)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("the reaper did not return within a second of its parent going")
	}
	if syscall.Kill(-done, 0) != nil {
		t.Error("a group taken off the list was killed")
	}
}

// startGroup runs sleep as the leader of a process group of its own, as
// RunBounded does, and reaps it in the background.
func startGroup(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	return pid
}

// alive is whether the process group still has a member that is running.
// A member already killed is reaped by the goroutine startGroup left.
func alive(pgid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) != nil {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

// When wt's end of the pipe closes — wt exited or was killed — the reaper
// kills every group still on its list and returns, soon.
func TestReapKillsTheGroupsLeftWhenThePipeCloses(t *testing.T) {
	left, done := startGroup(t), startGroup(t)
	r, w := io.Pipe()
	returned := make(chan struct{})
	go func() {
		reap(r, 0)
		close(returned)
	}()
	fmt.Fprintf(w, "+%d\n+%d\n-%d\n", left, done, done)
	start := time.Now()
	_ = w.Close()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("the reaper did not return within a second of the pipe closing")
	}
	if alive(left) {
		t.Error("a group on the list survived")
	}
	if syscall.Kill(-done, 0) != nil {
		t.Error("a group taken off the list was killed")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("reaping took %s", d)
	}
}

// tells records what RunBounded tells the reaper, and whether the process
// was still unreaped — a zombie, its pid not free for reuse — when it was
// taken off the list.
type tells struct {
	mu        sync.Mutex
	lines     []string
	unreaped  bool
	checkedAt int
}

func (c *tells) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		c.lines = append(c.lines, line)
		var pid int
		if _, err := fmt.Sscanf(line, "-%d", &pid); err == nil {
			c.unreaped = syscall.Kill(pid, 0) == nil
			c.checkedAt = pid
		}
	}
	return len(p), nil
}

// A writing command is put on the reaper's list when it starts and taken
// off after it exits but before it is reaped, so a group wt saw finish is
// never on the list with its id free for someone else. A read is never on
// it.
func TestRunBoundedTellsTheReaperBeforeReaping(t *testing.T) {
	c := &tells{}
	restore := setReaperWriter(c)
	defer restore()

	cmd := exec.Command("sh", "-c", "exit 0")
	if _, _, err := RunBounded(0, cmd); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	want := fmt.Sprintf("+%d,-%d", pid, pid)
	if got := strings.Join(c.lines, ","); got != want {
		t.Errorf("told %q, want %q", got, want)
	}
	if c.checkedAt != pid || !c.unreaped {
		t.Error("the group was taken off the list only after it was reaped")
	}

	c.lines = nil
	var out bytes.Buffer
	read := exec.Command("git", "--version")
	read.Stdout = &out
	if _, _, err := RunBounded(0, read); err != nil {
		t.Fatal(err)
	}
	if len(c.lines) != 0 {
		t.Errorf("a read was told to the reaper: %v", c.lines)
	}
}
