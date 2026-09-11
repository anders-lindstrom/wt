package wtsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// ScriptTimeout is the deadline a script gets when its rule sets no Timeout,
// for both --check and --resolve. A script that runs past it is treated as
// an error, never as a refusal: it did not answer the contract at all.
const ScriptTimeout = 60 * time.Second

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
// deadline, through git.RunBounded, which kills the script's whole process
// group when it fires. A script is trusted code from trunk; nothing here
// sandboxes it.
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

	cmd := exec.Command(exe, "--check", c.Path)
	cmd.Dir = s.Root
	cmd.Env = git.Environ("GIT_INDEX_FILE=" + index)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	timedOut, code, err := git.RunBounded(s.timeout(), cmd)
	if timedOut {
		return nil, timeoutError(s.Run, "--check", c.Path, s.timeout(), stderr.String())
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The script itself exited 0; only a background child it left
		// running kept stderr open past git.WaitDelay. That is not a
		// failure of --check.
		err = nil
	}
	switch {
	case err == nil:
		return nil, nil
	case code == 1:
		return nil, Refuse(c.Path, "%s does not claim it", s.Run)
	case code == 2:
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
	cmd := exec.Command(exe, "--resolve", path)
	cmd.Dir = wtPath
	cmd.Env = git.Environ("GIT_EDITOR=true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	timedOut, code, err := git.RunBounded(s.timeout(), cmd)
	if timedOut {
		return timeoutError(s.Run, "--resolve", path, s.timeout(), stderr.String())
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The script itself exited 0; only a background child it left
		// running kept stderr open past git.WaitDelay. Let the ls-files
		// check below decide whether --resolve actually did its job.
		err = nil
	}
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
	case code == 2:
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "refused without a reason"
		}
		return Refuse(path, "%s", reason)
	default:
		return fmt.Errorf("%s --resolve %s: %v: %s", s.Run, path, err, strings.TrimSpace(stderr.String()))
	}
}

// materialise extracts the directory holding run from trunk into a temporary
// directory with git archive, so the script and any sibling it sources come
// from trunk. It returns the executable's path.
//
// The archive goes to a file and tar extracts that file, one after the
// other, so each runs under gitDeadline in its own process group the way
// every git here does. A deadline that fires is reported as one, never as a
// script missing from trunk.
func materialise(root, trunk, run string) (exe string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wtsync-script-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	scriptDir := filepath.Dir(run)
	archive, tree := filepath.Join(dir, "script.tar"), filepath.Join(dir, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := gitEnv(root, nil, nil, "archive", "--format=tar", "-o", archive, trunk, scriptDir); err != nil {
		cleanup()
		// A git that ran and exited, rather than one the deadline cut off.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", nil, fmt.Errorf("script %s is not on %s: %w", run, trunk, err)
		}
		return "", nil, fmt.Errorf("reading script %s from %s: %w", run, trunk, err)
	}
	tar := exec.Command("tar", "-x", "-f", archive, "-C", tree)
	var tarErr bytes.Buffer
	tar.Stderr = &tarErr
	timedOut, _, runErr := git.RunBounded(gitDeadline, tar)
	if timedOut {
		cleanup()
		return "", nil, fmt.Errorf("extracting %s from %s: tar timed out after %s", scriptDir, trunk, gitDeadline)
	}
	if runErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("extracting %s from %s: %v: %s", scriptDir, trunk, runErr, strings.TrimSpace(tarErr.String()))
	}
	exe = filepath.Join(tree, run)
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

// gitDeadline is the deadline runGit and materialise actually use: GitTimeout,
// shortened only by a test proving that a git which never answers is cut off.
var gitDeadline = GitTimeout

// gitError is how a failed git reads in wtsync: what git said with how it
// exited in parentheses, or only how it exited when it said nothing. A
// deadline reads as git.Error words it. errors.As still reaches the
// *git.Error underneath.
type gitError struct{ err *git.Error }

func (e gitError) Error() string {
	if e.err.TimedOut {
		return e.err.Error()
	}
	if msg := strings.TrimSpace(e.err.Stderr); msg != "" {
		return msg + " (" + e.err.Err.Error() + ")"
	}
	return e.err.Err.Error()
}

func (e gitError) Unwrap() error { return e.err }

// runGit runs git in dir through git.Exec under gitDeadline, with extra
// environment and optional stdin, and returns stdout untrimmed: a blob's
// trailing newline is content. A failure keeps the stdout git wrote before
// it, for merge-file, whose exit status counts conflicts.
func runGit(dir string, env []string, stdin io.Reader, args ...string) ([]byte, error) {
	out, err := git.Exec(git.Opts{Dir: dir, Env: env, Stdin: stdin, Timeout: gitDeadline}, args...)
	var gerr *git.Error
	if errors.As(err, &gerr) {
		return out, gitError{gerr}
	}
	return out, err
}

// gitEnvAllow runs git in dir with extra environment and optional stdin,
// returning trimmed stdout and the exit status. A status equal to allow is
// an answer, not a failure: merge-base --is-ancestor and merge-tree both
// use one. Every other non-zero status is an error. allow < 0 allows none.
func gitEnvAllow(dir string, env []string, stdin io.Reader, allow int, args ...string) (string, int, error) {
	out, err := runGit(dir, env, stdin, args...)
	code, err := git.Answer(err, allow)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimRight(string(out), "\n"), code, nil
}

// gitEnv runs git and treats every non-zero status as a failure.
func gitEnv(dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	out, _, err := gitEnvAllow(dir, env, stdin, -1, args...)
	return out, err
}
