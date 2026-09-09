package wtsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ScriptTimeout is the deadline a script gets when its rule sets no Timeout,
// for both --check and --resolve. A script that runs past it is treated as
// an error, never as a refusal: it did not answer the contract at all.
const ScriptTimeout = 60 * time.Second

// scriptWaitDelay bounds how long runScript waits for the script's stdio to
// close once the deadline has killed it. A shell script's last command often
// runs as a forked child rather than replacing the shell, so killing only
// the shell can leave that child holding the stderr pipe open; runScript
// kills the whole process group to take it down too, and this is the
// fallback for the rare process that detaches from the group before that
// happens.
const scriptWaitDelay = 2 * time.Second

// Script is the escape hatch: an executable in the repository answering the
// three-verb contract. Its directory is materialised from trunk into a
// temporary directory, never run from a working tree, because a feature
// branch must not be able to change what runs. At triage time it is asked
// --check against a temporary index holding the conflict's three stages: the
// script sees exactly those blobs and nothing else of a rebase (no HEAD, no
// other index entries, mode 100644), which is the contract's limit. Resolve
// therefore returns nil bytes on success: the content is produced by
// --resolve in the worktree during a real run (ResolveInWorktree), against
// the worktree's real index. Both --check and --resolve run under a
// deadline. A script is trusted code from trunk; nothing here sandboxes it.
type Script struct {
	Root    string        // the main checkout, where git runs
	Trunk   string        // the ref the script is read from, e.g. origin/main
	Run     string        // the executable's path inside the repository
	Timeout time.Duration // zero means ScriptTimeout
}

// Name identifies this strategy in errors and reports.
func (Script) Name() string { return "script" }

// timeout returns the deadline for a script invocation: the rule's Timeout
// if set, otherwise ScriptTimeout.
func (s Script) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return ScriptTimeout
}

// runScript runs cmd, which must already carry a context deadline from
// exec.CommandContext, in its own process group so that a timeout kills any
// process the script forked, not just the script's own interpreter; see
// scriptWaitDelay for why that matters.
func runScript(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = scriptWaitDelay
	return cmd.Run()
}

// timeoutError formats a deadline-exceeded error, appending the script's
// stderr captured before it was killed when there is any; the buffer is
// quiescent once Wait has returned, so reading it here is safe.
func timeoutError(run, verb, path string, d time.Duration, stderr string) error {
	msg := fmt.Sprintf("%s %s %s: timed out after %s", run, verb, path, d)
	if reason := strings.TrimSpace(stderr); reason != "" {
		msg += ": " + reason
	}
	return errors.New(msg)
}

// Resolve checks the conflict against the script, through a temporary index
// holding only the conflict's three stages. See the type comment.
func (s Script) Resolve(c Conflict) ([]byte, error) {
	exe, cleanupExe, err := materialise(s.Root, s.Trunk, s.Run)
	if err != nil {
		return nil, err
	}
	defer cleanupExe()
	index, cleanup, err := tempIndex(s.Root, c)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--check", c.Path)
	cmd.Dir = s.Root
	cmd.Env = withEnv("GIT_INDEX_FILE=" + index)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = runScript(cmd)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, timeoutError(s.Run, "--check", c.Path, s.timeout(), stderr.String())
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The script itself exited 0; only a background child it left
		// running kept stderr open past scriptWaitDelay. That is not a
		// failure of --check.
		err = nil
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return nil, Refuse(c.Path, "%s does not claim it", s.Run)
	case errors.As(err, &exit) && exit.ExitCode() == 2:
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "refused without a reason"
		}
		return nil, Refuse(c.Path, "%s", reason)
	default:
		return nil, fmt.Errorf("%s --check %s: %v: %s", s.Run, c.Path, err, strings.TrimSpace(stderr.String()))
	}
}

// ResolveInWorktree runs `<script> --resolve <path>` in the worktree, against
// its real index, for a stopped rebase. The script is still read from trunk.
// Exit 0 means the script wrote and staged the file, which is verified; exit
// 2 is a refusal carrying stderr; anything else is an error.
func (s Script) ResolveInWorktree(wtPath, path string) error {
	exe, cleanup, err := materialise(s.Root, s.Trunk, s.Run)
	if err != nil {
		return err
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--resolve", path)
	cmd.Dir = wtPath
	cmd.Env = withEnv("GIT_EDITOR=true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = runScript(cmd)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return timeoutError(s.Run, "--resolve", path, s.timeout(), stderr.String())
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The script itself exited 0; only a background child it left
		// running kept stderr open past scriptWaitDelay. Let the ls-files
		// check below decide whether --resolve actually did its job.
		err = nil
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		out, lerr := gitEnv(wtPath, nil, nil, "ls-files", "-u", "--", path)
		if lerr != nil {
			return lerr
		}
		if out != "" {
			return fmt.Errorf("%s --resolve exited 0 but left %s unmerged", s.Run, path)
		}
		return nil
	case errors.As(err, &exit) && exit.ExitCode() == 2:
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "refused without a reason"
		}
		return Refuse(path, "%s", reason)
	default:
		return fmt.Errorf("%s --resolve %s: %v: %s", s.Run, path, err, strings.TrimSpace(stderr.String()))
	}
}

// withEnv copies the process environment and appends extra, dropping any
// existing entry for a key extra sets: some getenv implementations return the
// first match, not the last, so a stale GIT_INDEX_FILE ahead of ours in
// os.Environ() must not survive.
func withEnv(extra ...string) []string {
	keys := make(map[string]bool, len(extra))
	for _, kv := range extra {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			keys[kv[:i]] = true
		}
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+len(extra))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 && keys[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// isExit reports whether err is an *exec.ExitError with the given exit code.
func isExit(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// combineErr joins two processes' stderr into one message, archive's first,
// dropping either side that is empty.
func combineErr(archiveErr, tarErr *bytes.Buffer) string {
	a := strings.TrimSpace(archiveErr.String())
	t := strings.TrimSpace(tarErr.String())
	switch {
	case a != "" && t != "":
		return a + ": " + t
	case a != "":
		return a
	default:
		return t
	}
}

// materialise extracts the directory holding run from trunk into a temporary
// directory with git archive, so the script and any sibling it sources come
// from trunk. It returns the executable's path.
func materialise(root, trunk, run string) (exe string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wtsync-script-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	scriptDir := filepath.Dir(run)
	archive := exec.Command("git", "archive", "--format=tar", trunk, scriptDir)
	archive.Dir = root
	tar := exec.Command("tar", "-x", "-C", dir)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		cleanup()
		return "", nil, err
	}
	tar.Stdin = pipe
	// archive and tar run concurrently (tar reads the pipe as archive writes
	// it), each with its own exec.Cmd goroutine copying stderr: they must not
	// share one buffer, or writes from both race on it.
	var archiveErr, tarErr bytes.Buffer
	archive.Stderr = &archiveErr
	tar.Stderr = &tarErr
	if err := tar.Start(); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := archive.Run(); err != nil {
		_ = tar.Wait()
		cleanup()
		return "", nil, fmt.Errorf("script %s is not on %s: %s", run, trunk, combineErr(&archiveErr, &tarErr))
	}
	if err := tar.Wait(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extracting %s from %s: %s", scriptDir, trunk, combineErr(&archiveErr, &tarErr))
	}
	exe = filepath.Join(dir, run)
	if info, err := os.Stat(exe); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		cleanup()
		return "", nil, fmt.Errorf("script %s is not an executable on %s", run, trunk)
	}
	return exe, cleanup, nil
}

// tempIndex writes a private index holding the three stages of the conflict
// and nothing else. The blobs are written to the object store, which is the
// only side effect, and one git already tolerates.
func tempIndex(root string, c Conflict) (index string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wtsync-index-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	index = filepath.Join(dir, "index")
	var info strings.Builder
	for stage, data := range map[int][]byte{1: c.Base, 2: c.Trunk, 3: c.Branch} {
		oid, err := hashObject(root, data)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		fmt.Fprintf(&info, "100644 %s %d\t%s\n", oid, stage, c.Path)
	}
	if _, err := gitEnv(root, []string{"GIT_INDEX_FILE=" + index}, strings.NewReader(info.String()), "update-index", "--index-info"); err != nil {
		cleanup()
		return "", nil, err
	}
	return index, cleanup, nil
}

// hashObject writes data to the object store and returns its id.
func hashObject(root string, data []byte) (string, error) {
	return gitEnv(root, nil, bytes.NewReader(data), "hash-object", "-w", "--stdin")
}

// GitTimeout is the deadline one git invocation gets here. Nothing wtsync
// runs is interactive: a git still going after this is stuck, not slow, and
// a run that waits on it holds its locks the whole time.
const GitTimeout = 10 * time.Minute

// gitEnv runs git in dir with extra environment and optional stdin, returning
// trimmed stdout. internal/git.Run has no place for either.
//
// GIT_TERMINAL_PROMPT=0 goes in first so a repository wanting credentials
// fails rather than blocking on a prompt no unattended run can answer; the
// deadline and the process group come from runScript.
func gitEnv(dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = withEnv(append([]string{"GIT_TERMINAL_PROMPT=0"}, env...)...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := runScript(cmd)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s: timed out after %s", strings.Join(args, " "), GitTimeout)
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s (%w)", msg, err)
		}
		return "", err
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
