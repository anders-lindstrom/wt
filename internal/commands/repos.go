package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ReposOptions is how `wt repos` prints: a table, or paths alone for a script.
type ReposOptions struct {
	Paths bool
}

// Repos lists the repositories wt manages under the roots, or the ones a
// selection names, grouped by root. It reads the disk only: no fetch, no
// GitHub.
func Repos(u *config.User, sel Selection, opts ReposOptions, w io.Writer) error {
	set, err := SelectRepos(u, sel)
	if err != nil {
		return err
	}
	if opts.Paths {
		for _, t := range set.Repos {
			if t.Problem == "" {
				fmt.Fprintln(w, t.Path)
			}
		}
		return nil
	}
	home, _ := os.UserHomeDir()
	if len(sel.Profiles) > 0 {
		fmt.Fprintf(w, "profile %s\n", strings.Join(sel.Profiles, ", "))
		_ = printTable(w, repoRows(set.Repos, home))
		return problemsIn(set.Repos)
	}
	roots, origin := RootsFor(u)
	fmt.Fprintf(w, "roots from %s\n", origin)
	for _, root := range roots {
		if len(sel.Roots) > 0 && !slices.Contains(sel.Roots, root.Name) {
			continue
		}
		var in []RepoTarget
		for _, t := range set.Repos {
			if t.Root == root.Name {
				in = append(in, t)
			}
		}
		fmt.Fprintf(w, "\n%s  %s  (%s)\n", root.Name, abbreviateHome(root.Path, home), repoCount(len(in)))
		if _, err := os.Stat(root.Path); err != nil {
			fmt.Fprintln(w, "  ! the directory is not there")
			continue
		}
		_ = printTable(w, repoRows(in, home))
	}
	if n := len(set.Unmanaged); n > 0 {
		fmt.Fprintf(w, "\n%s under these roots %s not managed by wt; wt init in one sets it up.\n",
			checkoutCount(n), isAre(n))
	}
	return nil
}

// repoRows is one row per repository: its name, how many worktrees it has
// beside the main checkout, whether wt sync is set up there, and where it is.
// A repository whose wt configuration does not parse says so instead.
func repoRows(targets []RepoTarget, home string) [][]string {
	statuses := eachRepo(targets, repoParallelism, repoStatus)
	var rows [][]string
	for i, t := range targets {
		s := statuses[i]
		switch {
		// The problem goes last, where its length widens no column.
		case t.Problem != "":
			rows = append(rows, []string{"  ! " + t.Name, "", "", abbreviateHome(t.Path, home) + "  " + t.Problem})
		case s.problem != "":
			rows = append(rows, []string{"  ! " + t.Name, "", "", abbreviateHome(t.Path, home) + "  " + s.problem})
		default:
			rows = append(rows, []string{"  " + t.Name, s.worktrees, s.sync, abbreviateHome(t.Path, home)})
		}
	}
	return rows
}

// status is what the listing says about one repository: its worktrees,
// whether trunk declares wt sync, and a configuration wt cannot read.
type status struct {
	worktrees, sync, problem string
}

// repoStatus reads a repository's status from the disk: its configuration,
// its worktrees, and the .wt-sync.yaml on trunk as last fetched.
func repoStatus(t RepoTarget) status {
	if t.Problem != "" {
		return status{}
	}
	ctx := OpenLenient(t.Path, io.Discard)
	if ctx == nil {
		return status{problem: "not a git repository"}
	}
	if ctx.ConfigError != nil {
		return status{problem: "wt configuration: " + oneLine(ctx.ConfigError.Error()) + "; wt doctor in it"}
	}
	s := status{worktrees: worktreesIn(t.Path), sync: "sync set up"}
	if _, ok := ctx.Repo.ResolveRef("refs/remotes/origin/" + ctx.Config.MainBranch); !ok {
		// wt sync reads the declaration from origin's trunk; with none
		// fetched there is nothing to read it from.
		s.sync = "sync unknown: origin/" + ctx.Config.MainBranch + " never fetched"
		return s
	}
	if _, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, ctx.Config.MainBranch); errors.Is(err, wtsync.ErrNoConfig) {
		s.sync = "no sync"
	} else if err != nil {
		s.sync = "sync broken: " + oneLine(err.Error())
	}
	return s
}

// worktreesIn counts a repository's worktrees beside its main checkout.
func worktreesIn(path string) string {
	r, err := repo.Discover(path)
	if err != nil {
		return "?"
	}
	wts, err := r.Worktrees()
	if err != nil {
		return "?"
	}
	switch n := len(wts) - 1; n {
	case 0:
		return "no worktrees"
	case 1:
		return "1 worktree"
	default:
		return strconv.Itoa(n) + " worktrees"
	}
}

// problemsIn fails a listing that found a profile entry wt cannot work on,
// once every row is printed.
func problemsIn(targets []RepoTarget) error {
	n := 0
	for _, t := range targets {
		if t.Problem != "" {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return fmt.Errorf("%d of the profile's repositories cannot be worked on; wt doctor says the same", n)
}

func repoCount(n int) string {
	if n == 1 {
		return "1 repository"
	}
	return strconv.Itoa(n) + " repositories"
}

func checkoutCount(n int) string {
	if n == 1 {
		return "1 checkout"
	}
	return strconv.Itoa(n) + " checkouts"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
