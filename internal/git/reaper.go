package git

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The reaper stops what wt started when wt itself is killed in a way it
// cannot handle. RunBounded gives every command a process group of its own,
// which its deadline and a handled signal kill as a whole; SIGKILL leaves
// those groups running, writing into a worktree nobody is watching. So the
// first writing command wt starts also starts one reaper: wt's own binary,
// named "wt reaper", in a process group of its own, reading the other end of
// a pipe. wt tells it "+<pgid>" as each writing command starts and
// "-<pgid>" once it has exited but before it is reaped, so an id the reaper
// holds is never free for another group. When wt is gone however it went —
// its end of the pipe closes, or its parent changes — the reaper kills the
// groups still listed and exits.
//
// It holds none of wt's stdio, so a `wt … | jq` pipeline ends when wt does.
// A command started and killed before wt has told the reaper escapes it.

const (
	reaperName = "wt reaper"
	reaperEnv  = "WT_REAPER=1"
	// drainGrace is how long the reaper, finding wt gone with the pipe still
	// open, waits for the list wt wrote before acting on it.
	drainGrace = 500 * time.Millisecond
)

var reaper = struct {
	sync.Mutex
	// exe is wt's binary, set by UseReaper; empty means no reaper.
	exe string
	// w is the pipe to the running reaper; nil before it starts, or when it
	// could not be started or stopped listening.
	w       io.Writer
	started bool
}{}

// UseReaper lets the first writing command RunBounded starts also start a
// reaper from exe, wt's own binary. Without it — as in tests — there is none.
func UseReaper(exe string) {
	reaper.Lock()
	reaper.exe = exe
	reaper.Unlock()
}

// IsReaper is whether this process was started as the reaper.
func IsReaper() bool {
	return len(os.Args) == 1 && os.Args[0] == reaperName && os.Getenv("WT_REAPER") == "1"
}

// Reap is the reaper's whole life: read wt's list from stdin until wt is
// gone, then kill what is left on it.
func Reap() {
	closeInherited()
	signal.Ignore(syscall.SIGHUP, syscall.SIGINT, syscall.SIGPIPE)
	reap(os.Stdin, os.Getppid())
}

// closeInherited closes every descriptor above stderr that wt's own parent
// passed down — a caller's pipe on fd 3, say — so the reaper never keeps a
// pipeline open after wt is gone. Go opens its own descriptors
// close-on-exec, so one without that flag was inherited.
func closeInherited() {
	for fd := 3; fd < 1024; fd++ {
		if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil && flags&unix.FD_CLOEXEC == 0 {
			_ = unix.Close(fd)
		}
	}
}

// reap reads "+pgid" and "-pgid" lines from in until it ends, or until the
// process's parent is no longer parent (0: do not watch) and what was
// written before has been read, then sends every
// group still listed SIGTERM, waits up to TermGrace, and sends SIGKILL to
// what remains. A group that no longer exists is not signalled.
func reap(in io.Reader, parent int) {
	var mu sync.Mutex
	listed := map[int]bool{}
	gone := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			line := sc.Text()
			if len(line) < 2 {
				continue
			}
			pgid, err := strconv.Atoi(line[1:])
			if err != nil || pgid <= 1 {
				continue
			}
			mu.Lock()
			if line[0] == '+' {
				listed[pgid] = true
			} else {
				delete(listed, pgid)
			}
			mu.Unlock()
		}
		close(gone)
	}()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case <-gone:
			break wait
		case <-tick.C:
			if parent != 0 && os.Getppid() != parent {
				// wt is gone, so all it wrote is in the pipe: give the
				// reader a moment to take it, in case something else holds
				// the pipe open and it never ends.
				select {
				case <-gone:
				case <-time.After(drainGrace):
				}
				break wait
			}
		}
	}
	mu.Lock()
	var left []int
	for pgid := range listed {
		if syscall.Kill(-pgid, 0) == nil {
			left = append(left, pgid)
		}
	}
	mu.Unlock()
	for _, pgid := range left {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(TermGrace)
	for len(left) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		left = existing(left)
	}
	for _, pgid := range existing(left) {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

func existing(pgids []int) []int {
	var out []int
	for _, pgid := range pgids {
		if syscall.Kill(-pgid, 0) == nil {
			out = append(out, pgid)
		}
	}
	return out
}

// tellReaper puts the group pgid on the reaper's list (sign '+') or takes
// it off ('-'), starting the reaper first if none runs yet. Best effort: a
// reaper that cannot be started or reached is given up on.
func tellReaper(sign byte, pgid int) bool {
	reaper.Lock()
	defer reaper.Unlock()
	if reaper.w == nil && !reaper.started && reaper.exe != "" && sign == '+' {
		reaper.started = true
		reaper.w = startReaper(reaper.exe)
	}
	if reaper.w == nil {
		return false
	}
	if _, err := fmt.Fprintf(reaper.w, "%c%d\n", sign, pgid); err != nil {
		reaper.w = nil
		return false
	}
	return true
}

// startReaper starts exe as the reaper and returns the pipe to it, nil if it
// did not start. wt's end of the pipe is close-on-exec, as every descriptor
// Go opens is, so no other child holds it open after wt is gone. The reaper
// is never waited for: it outlives wt by design, and exits on its own.
func startReaper(exe string) io.Writer {
	r, w, err := os.Pipe()
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
	cmd := &exec.Cmd{Path: exe, Args: []string{reaperName}, Stdin: r, Env: Environ(reaperEnv),
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return nil
	}
	return w
}

// setReaperWriter makes w the reaper's pipe, for a test; the returned
// function puts back what was there.
func setReaperWriter(w io.Writer) func() {
	reaper.Lock()
	prev, prevStarted := reaper.w, reaper.started
	reaper.w, reaper.started = w, true
	reaper.Unlock()
	return func() {
		reaper.Lock()
		reaper.w, reaper.started = prev, prevStarted
		reaper.Unlock()
	}
}

// Writes is whether the command args (argv, args[0] the program), run with
// env, can write or run long enough to be worth a reaper. The answer is no
// only for what is known to read: git's reading subcommands — and those that
// only add objects, which is how wt status simulates a rebase — and the
// GitHub, Claude, ps, lsof and Docker queries wt makes. Anything else — a git that writes or is not
// recognised, a script, a build — counts as writing.
func Writes(args, env []string) bool {
	if len(args) == 0 {
		return true
	}
	switch filepath.Base(args[0]) {
	case "git":
		return gitWrites(args[1:], env)
	case "gh":
		pos := positionals(args[1:])
		return len(pos) >= 2 && pos[0] == "pr" && pos[1] == "checkout"
	case "claude", "ps", "lsof", "docker":
		return false
	}
	return true
}

// gitReads are git subcommands that never write the repository.
var gitReads = map[string]bool{
	"rev-parse": true, "for-each-ref": true, "rev-list": true, "show-ref": true, "cat-file": true,
	"show": true, "log": true, "merge-base": true, "ls-tree": true, "ls-files": true, "cherry": true,
	"check-ref-format": true, "check-ignore": true, "status": true, "diff": true, "name-rev": true,
	"describe": true, "var": true, "count-objects": true, "patch-id": true, "version": true,
	"diff-tree": true, "diff-index": true, "diff-files": true, "grep": true, "blame": true,
	"shortlog": true, "range-diff": true, "ls-remote": true,
	// Objects only: nothing a ref or a worktree sees.
	"merge-tree": true, "commit-tree": true, "hash-object": true, "write-tree": true,
}

func gitWrites(args, env []string) bool {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		switch args[i] {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace":
			i++
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	sub, rest := args[i], args[i+1:]
	pos := positionals(rest)
	switch {
	case gitReads[sub]:
		return false
	case sub == "read-tree":
		// Into a temporary index it is a simulation; into a worktree's own
		// index it changes what that worktree stages.
		for _, kv := range env {
			if strings.HasPrefix(kv, "GIT_INDEX_FILE=") {
				return false
			}
		}
		return true
	case sub == "worktree":
		return len(pos) == 0 || pos[0] != "list"
	case sub == "config":
		for _, a := range rest {
			switch a {
			case "--get", "--get-all", "--get-regexp", "--list", "-l":
				return false
			}
		}
		return true
	case sub == "symbolic-ref":
		return len(pos) != 1
	case sub == "remote":
		return len(pos) != 0 && pos[0] != "get-url" && pos[0] != "show"
	case sub == "reflog":
		return len(pos) != 0 && pos[0] != "show"
	case sub == "stash":
		return len(pos) == 0 || (pos[0] != "list" && pos[0] != "show")
	case sub == "branch":
		for _, a := range rest {
			if a == "--unset-upstream" || a == "--edit-description" {
				return true
			}
		}
		return len(pos) != 0
	}
	return true
}

// positionals is args without its options.
func positionals(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}
