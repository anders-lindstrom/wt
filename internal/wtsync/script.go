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
)

// Script is the escape hatch: an executable in the repository answering the
// three-verb contract. Its directory is materialised from trunk into a
// temporary directory, never run from a working tree, because a feature
// branch must not be able to change what runs. At triage time it is asked
// --check against a temporary index holding the conflict's three stages: the
// script sees exactly those blobs and nothing else of a rebase (no HEAD, no
// other index entries, mode 100644), which is the contract's limit. Resolve
// therefore returns nil bytes on success: the content is produced by
// --resolve during a real rebase, which the next plan performs. A script is
// trusted code from trunk; nothing here sandboxes it.
type Script struct {
	Root  string // the main checkout, where git runs
	Trunk string // the ref the script is read from, e.g. origin/main
	Run   string // the executable's path inside the repository
}

// Name identifies this strategy in errors and reports.
func (Script) Name() string { return "script" }

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
	cmd.Env = withEnv("GIT_INDEX_FILE=" + index)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
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

// gitEnv runs git in dir with extra environment and optional stdin, returning
// trimmed stdout. internal/git.Run has no place for either.
func gitEnv(dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = withEnv(env...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
