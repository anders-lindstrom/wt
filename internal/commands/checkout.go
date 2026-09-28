package commands

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

var unsafeInWorkName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// WorkNameFromBranch derives a directory-safe work name from a branch name.
// A worktree branch loses its type prefix — fix_wt/login-crash becomes
// login-crash rather than fix_wt-login-crash — and anything else has unsafe
// runs collapsed to a single dash.
func WorkNameFromBranch(branch, suffix string) string {
	work := naming.StripPrefix(branch, suffix)
	work = unsafeInWorkName.ReplaceAllString(work, "-")
	return strings.Trim(work, "-")
}

// Where the branch wt checkout puts a worktree on comes from.
const (
	// SourceLocal is a local branch, checked out as it is.
	SourceLocal = "local"
	// SourceRemote is a remote-tracking ref: the local branch is created at
	// its commit, tracking it.
	SourceRemote = "remote"
)

// checkoutTarget is what wt checkout's argument names.
type checkoutTarget struct {
	// Branch is the local branch the worktree goes on.
	Branch string
	Source string
	// Commit is the local branch's tip, or the remote-tracking ref's.
	Commit string
	// Remote and RemoteRef are the remote and its remote-tracking ref,
	// refs/remotes/<remote>/<branch>, for SourceRemote.
	Remote, RemoteRef string
}

// upstream is RemoteRef as a person writes it: origin/topic.
func (t checkoutTarget) upstream() string {
	return strings.TrimPrefix(t.RemoteRef, "refs/remotes/")
}

// resolveCheckout reads what arg names, from local state alone: the
// remote-tracking refs are as the last fetch left them. In order: a local
// branch of exactly that name; <remote>/<branch> for a configured remote
// that has the branch; a bare <branch> exactly one remote has, or
// checkout.defaultRemote's when several do. A branch wt would create from a
// remote must not exist locally. Branch is arg when nothing matched.
func resolveCheckout(ctx *Context, arg string) (checkoutTarget, *Problem) {
	t := checkoutTarget{Branch: arg}
	if arg == "" {
		return t, &Problem{Code: ProblemInvalidName, Message: "no branch given"}
	}
	if oid, ok := ctx.Repo.ResolveRef("refs/heads/" + arg); ok && ctx.Repo.BranchExists(arg) {
		t.Source, t.Commit = SourceLocal, oid
		return t, nil
	}
	remotes, _ := ctx.Repo.Remotes()
	tracking, _ := ctx.Repo.RemoteTrackingRefs()
	candidate := func(remote, branch string) (checkoutTarget, bool) {
		ref := "refs/remotes/" + remote + "/" + branch
		oid, ok := tracking[ref]
		if !ok || branch == "HEAD" {
			return checkoutTarget{}, false
		}
		return checkoutTarget{Branch: branch, Source: SourceRemote, Commit: oid, Remote: remote, RemoteRef: ref}, true
	}
	found := func(c checkoutTarget) (checkoutTarget, *Problem) {
		if ctx.Repo.BranchExists(c.Branch) {
			return c, &Problem{Code: ProblemBranchExists, Message: fmt.Sprintf(
				"branch %s already exists here; `wt checkout %s` puts a worktree on it", c.Branch, c.Branch)}
		}
		return c, nil
	}

	// <remote>/<branch>; of two remotes that both prefix it, the longer.
	prefixed := ""
	for _, remote := range remotes {
		if rest, ok := strings.CutPrefix(arg, remote+"/"); ok && rest != "" && len(remote) > len(prefixed) {
			prefixed = remote
		}
	}
	if prefixed != "" {
		if c, ok := candidate(prefixed, strings.TrimPrefix(arg, prefixed+"/")); ok {
			return found(c)
		}
	}

	var hits []checkoutTarget
	for _, remote := range remotes {
		if c, ok := candidate(remote, arg); ok {
			hits = append(hits, c)
		}
	}
	if len(hits) > 1 {
		def, _ := git.Run(ctx.Repo.MainRoot, "config", "--get", "checkout.defaultRemote")
		for _, c := range hits {
			if c.Remote == strings.TrimSpace(def) {
				return found(c)
			}
		}
		var names []string
		for _, c := range hits {
			names = append(names, c.upstream())
		}
		return t, &Problem{Code: ProblemBranchAmbiguous, Message: fmt.Sprintf(
			"%s is on more than one remote: %s; name one, as `wt checkout %s`, or set checkout.defaultRemote",
			arg, strings.Join(names, ", "), names[0])}
	}
	if len(hits) == 1 {
		return found(hits[0])
	}
	if prefixed != "" {
		return t, &Problem{Code: ProblemRemoteBranchMissing, Message: fmt.Sprintf(
			"%s has no branch %s as last fetched; `git fetch %s` if it is new",
			prefixed, strings.TrimPrefix(arg, prefixed+"/"), prefixed)}
	}
	return t, &Problem{Code: ProblemBranchMissing, Message: fmt.Sprintf(
		"branch %s does not exist here or on a remote as last fetched; `git fetch` if it is new, `wt new` creates one", arg)}
}

// Checkout puts a worktree on a branch that exists — for reviewing a pull
// request or picking up work that already has a branch — locally, or on a
// remote as last fetched, in which case the local branch is created at the
// remote-tracking commit, tracking it. It never invents a branch no one has:
// that is a mistake worth reporting.
func Checkout(ctx *Context, arg, work string, opts NewOptions, w io.Writer) (string, error) {
	t, prob := resolveCheckout(ctx, arg)
	if prob != nil {
		return "", errors.New(prob.Message)
	}
	if work == "" {
		work = WorkNameFromBranch(t.Branch, ctx.Scheme().Suffix)
		if work == "" {
			return "", fmt.Errorf("could not derive a work name from branch %q", t.Branch)
		}
		if work != t.Branch {
			fmt.Fprintf(w, "Using work name %q for branch %s\n", work, t.Branch)
		}
	}

	path := ctx.Scheme().Dir(ctx.Config.DefaultType, work)
	return addAndProvision(ctx, path, checkoutAdd(ctx, path, t, nil, w), opts, w, nil)
}

// checkoutAdd is wt checkout's add: a worktree on the local branch as it is,
// or on one created from the remote-tracking ref first, which j records
// before the worktree is added. A local branch that
// appeared since the plan is never moved: that fails with
// repo.ErrBranchExists, nothing changed.
func checkoutAdd(ctx *Context, path string, t checkoutTarget, j *CreateJournal, w io.Writer) func() error {
	return func() error {
		if t.Source != SourceRemote {
			fmt.Fprintf(w, "Checking out %s at %s\n", t.Branch, path)
			return ctx.Repo.AddExistingWorktree(path, t.Branch)
		}
		fmt.Fprintf(w, "Creating %s at %s (from %s, tracking it)\n", t.Branch, path, t.upstream())
		if err := ctx.Repo.CreateTrackingBranch(t.Branch, t.Commit, t.Remote, "refs/heads/"+t.Branch); err != nil {
			if errors.Is(err, repo.ErrBranchExists) {
				return fmt.Errorf("%w; branch %s appeared here since the plan and was left as it is; "+
					"`wt checkout %s` puts a worktree on it", err, t.Branch, t.Branch)
			}
			return err
		}
		j.branchMade(ctx)
		return ctx.Repo.AddExistingWorktree(path, t.Branch)
	}
}
