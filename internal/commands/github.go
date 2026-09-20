package commands

import (
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/github"
)

// How these commands reach the GitHub CLI. Variables so a test can put a
// state in front of them without a gh on the machine running it.
var (
	findGitHub   = github.Find
	gitHubRemote = github.RemoteOf
)

// gitHub is a usable GitHub CLI and the repository it answers for.
type gitHub struct {
	CLI    github.CLI
	Remote github.Remote
}

// openGitHub runs the gates the integration stands on and returns, for the
// first that failed, one line saying how to fix it: the person's own setting,
// gh on the PATH, a GitHub remote, then gh's login — the remote first because
// it names the host to be logged in to.
//
// authenticated costs a process. `wt list` passes false: it has one bounded
// call to spend, and no column is its answer to anything going wrong.
func openGitHub(ctx *Context, authenticated bool) (gitHub, error) {
	if !ctx.UserConfig().GitHub {
		return gitHub{}, fmt.Errorf("GitHub is off in your wt config; turn it on with `wt config set %s true`",
			config.UserKeyGitHub)
	}
	cli, ok := findGitHub()
	if !ok {
		return gitHub{}, fmt.Errorf("gh is not on your PATH; install the GitHub CLI (https://cli.github.com), " +
			"or turn this off with `wt config set " + config.UserKeyGitHub + " false`")
	}
	remote, ok := gitHubRemote(ctx.Repo.MainRoot)
	if !ok {
		return gitHub{}, fmt.Errorf("%s has no GitHub remote; `wt pr` works on repositories hosted on GitHub",
			ctx.Repo.Name)
	}
	if authenticated {
		if err := cli.Authenticated(remote.Host); err != nil {
			return gitHub{}, fmt.Errorf("gh is not logged in to %s; run `gh auth login --hostname %s`",
				remote.Host, remote.Host)
		}
	}
	return gitHub{CLI: cli, Remote: remote}, nil
}

// doctorGitHub reports whether `wt pr` would reach GitHub here, and where it
// would stop if it would not. Nothing in this section is counted as a
// problem: a machine without gh is an ordinary machine.
func doctorGitHub(ctx *Context, w io.Writer) {
	fmt.Fprintln(w, "GitHub:")
	if !ctx.UserConfig().GitHub {
		fmt.Fprintf(w, "  - off in your wt config; turn it on with `wt config set %s true`\n",
			config.UserKeyGitHub)
		return
	}
	cli, ok := findGitHub()
	if !ok {
		fmt.Fprintln(w, "  - no gh on the PATH; `wt pr` is inactive and `wt list` shows no pull requests")
		return
	}
	version := cli.Version()
	if version == "" {
		version = "version unknown"
	}
	fmt.Fprintf(w, "  ✓ %s (%s)\n", cli.Exe, version)

	remote, ok := gitHubRemote(ctx.Repo.MainRoot)
	if !ok {
		fmt.Fprintf(w, "  - no GitHub remote for %s; `wt pr` is inactive here\n", ctx.Repo.Name)
		return
	}
	// A login gh answered about is one thing; a `gh auth status` that timed
	// out, or failed for its own reasons, is another, and reporting the
	// second as the first sends you off to log in again for nothing.
	switch err := cli.Authenticated(remote.Host); {
	case err == nil:
		fmt.Fprintf(w, "  ✓ %s on %s — gh is logged in\n", remote.Slug, remote.Host)
	case github.NotLoggedIn(err):
		fmt.Fprintf(w, "  - %s is on %s, which gh is not logged in to; run `gh auth login --hostname %s`\n",
			remote.Slug, remote.Host, remote.Host)
		return
	default:
		fmt.Fprintf(w, "  - could not check gh's login for %s: %v\n", remote.Host, err)
		return
	}
}
