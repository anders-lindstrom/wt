// Package github reads pull requests through the GitHub CLI.
//
// wt stores no credentials and never logs anyone in: whatever `gh` is
// authenticated as is what wt sees. Nothing here writes to GitHub — the only
// command that changes anything is `gh pr checkout`, and what it changes is
// the local checkout.
package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// The deadlines bounding the CLI. ListDeadline bounds the branch lookup
// behind `wt list` and `wt status`; a call that overruns it costs the column
// and one line on stderr. A checkout may have a whole branch to fetch.
var (
	ListDeadline     = 2 * time.Second
	readDeadline     = 15 * time.Second
	authDeadline     = 10 * time.Second
	checkoutDeadline = 10 * time.Minute
)

// CLI is a located GitHub command line.
type CLI struct{ Exe string }

// Find locates the GitHub CLI on the PATH.
func Find() (CLI, bool) {
	exe, err := exec.LookPath("gh")
	if err != nil {
		return CLI{}, false
	}
	return CLI{Exe: exe}, true
}

// Version is the CLI's own version, first line only, or "" when it will not
// say. `gh --version` prints the release URL under it.
func (c CLI) Version() string {
	out, err := c.run("", readDeadline, "--version")
	if err != nil {
		return ""
	}
	// "gh version 2.100.0 (2026-09-03)", with the release URL on the next
	// line. The number alone is what a report wants.
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	version, _, _ := strings.Cut(strings.TrimPrefix(first, "gh version "), " ")
	return strings.TrimSpace(version)
}

// Authenticated reports whether gh holds a login for host. It costs a
// process, so only `wt doctor` asks it: everything else reads the login out
// of the call it was making anyway, through NotLoggedIn.
func (c CLI) Authenticated(host string) error {
	_, err := c.run("", authDeadline, "auth", "status", "--hostname", host)
	return err
}

// listFields is what a pull request is read as, in `gh pr list --json`'s
// spelling. It matches prFields, so one PR struct decodes both answers.
var listFields = []string{
	"number", "title", "author", "headRefName", "baseRefName", "headRefOid", "isDraft", "state",
	"reviewDecision", "isCrossRepository", "headRepositoryOwner", "url",
}

// OpenWithChecks reads the repository's open pull requests and what the checks
// on each head commit say, newest first: the wide view `wt pr list` prints.
// The rollup roughly doubles the call, so nothing on a deadline asks for it.
// dir is any checkout of the repository: gh reads the remote from it.
func (c CLI) OpenWithChecks(dir string, limit int) ([]PR, error) {
	fields := append(append([]string{}, listFields...), "statusCheckRollup")
	out, err := c.run(dir, readDeadline, "pr", "list", "--state", "open",
		"--limit", fmt.Sprint(limit), "--json", strings.Join(fields, ","))
	if err != nil {
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("gh pr list --json: %w", err)
	}
	return prs, nil
}

// View reads one pull request by number, whatever state it is in.
func (c CLI) View(dir string, number int) (PR, error) {
	out, err := c.run(dir, readDeadline, "pr", "view", fmt.Sprint(number),
		"--json", strings.Join(listFields, ","))
	if err != nil {
		return PR{}, err
	}
	var pr PR
	if err := json.Unmarshal(out, &pr); err != nil {
		return PR{}, fmt.Errorf("gh pr view --json: %w", err)
	}
	return pr, nil
}

// OpenWeb asks gh to open a pull request in a browser. Nothing is written to
// GitHub: --web resolves a URL and hands it to the desktop.
func (c CLI) OpenWeb(dir string, number int) error {
	_, err := c.run(dir, readDeadline, "pr", "view", fmt.Sprint(number), "--web")
	return err
}

// CheckoutInto runs `gh pr checkout` with dir as its working directory, so
// the branch, its remote and its push configuration are set up the way gh
// does it; nothing else gets a fork's push remote right. gh switches the
// branch in dir, so dir must have nothing to lose.
func (c CLI) CheckoutInto(dir string, number int) error {
	_, err := c.run(dir, checkoutDeadline, "pr", "checkout", fmt.Sprint(number))
	return err
}

// run executes the CLI under a deadline, through the same bounded runner git
// calls use, so the deadline and an interrupt take down whatever it forked.
// GH_PROMPT_DISABLED makes a call that would prompt fail instead of waiting.
func (c CLI) run(dir string, deadline time.Duration, args ...string) ([]byte, error) {
	if deadline <= 0 {
		deadline = readDeadline
	}
	cmd := exec.Command(c.Exe, args...)
	cmd.Dir = dir
	cmd.Env = git.Environ("GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	timedOut, _, err := git.RunBounded(deadline, cmd)
	if timedOut {
		return nil, fmt.Errorf("%s did not answer within %s", called(args), deadline)
	}
	if err != nil {
		said := stderr.String()
		return nil, &callError{call: called(args), reason: reason(said, err), first: firstLine(said)}
	}
	return stdout.Bytes(), nil
}

// callError is a gh that ran and failed.
type callError struct {
	call string
	// reason is the line of stderr that says what went wrong.
	reason string
	// first is stderr's first line, which is where gh asks for a login.
	// NotLoggedIn reads it, so a reason taken from further down cannot hide it.
	first string
}

func (e *callError) Error() string { return e.call + " failed: " + e.reason }

// called is the call as a message names it: the subcommand and its arguments,
// without the flags. The --json field list alone runs to 200 characters.
func called(args []string) string {
	kept := []string{"gh"}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		kept = append(kept, a)
	}
	if len(kept) == 1 && len(args) > 0 {
		kept = append(kept, args[0])
	}
	return strings.Join(kept, " ")
}

// reason is the line of gh's stderr that says what went wrong. Only a gh
// that ran and exited has one.
//
// `gh pr checkout` passes git's stderr through, so the error sits under
// git's notices ("Previous HEAD position was ...", "From github.com:...") and
// above gh's own "failed to run git: exit status 128". The last line git
// marked as an error is the one to report; without one it is a fetch's
// rejected ref, and failing that the first line, which is where gh writes
// "could not determine base repository" and the like.
func reason(stderr string, err error) string {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return err.Error()
	}
	var marked, rejected string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "fatal: "), strings.HasPrefix(line, "error: "):
			marked = line
		case strings.HasPrefix(line, "! [rejected]"), strings.HasPrefix(line, "! [remote rejected]"):
			rejected = strings.Join(strings.Fields(line), " ")
		}
	}
	for _, line := range []string{marked, rejected, firstLine(stderr)} {
		if line != "" {
			return line
		}
	}
	return err.Error()
}

// firstLine is the first line of s that is not blank, trimmed.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// loginWanted is how gh says it holds no credentials for the host. Each
// subcommand words it differently, so the test is on the phrases they share.
// "authentication token" is not among them: gh writes it about a token that
// is there and lacks a scope, which is a fault worth reporting.
var loginWanted = []string{"gh auth login", "not logged in", "not logged into"}

// NotLoggedIn reports whether a call failed for want of a login rather than
// for want of an answer: a machine with no GitHub set up is nothing to report.
// Nothing checks the login up front, so this is read off the call that was
// being made anyway.
func NotLoggedIn(err error) bool {
	if err == nil {
		return false
	}
	said := strings.ToLower(err.Error())
	var call *callError
	if errors.As(err, &call) {
		said += "\n" + strings.ToLower(call.first)
	}
	for _, phrase := range loginWanted {
		if strings.Contains(said, phrase) {
			return true
		}
	}
	return false
}
