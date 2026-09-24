package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// doctorRepos checks the roots and profiles every multi-repository command
// reads: each root is a directory and none sits inside another, and each
// profile entry is still the main checkout of a repository wt manages, inside
// one of the roots.
func doctorRepos(u *config.User, w io.Writer, report func(string, ...any)) {
	home, _ := os.UserHomeDir()
	roots, origin := RootsFor(u)
	fmt.Fprintf(w, "Repositories (roots from %s):\n", origin)
	for i, r := range roots {
		info, err := os.Stat(r.Path)
		switch {
		case err != nil && origin == "default":
			// Nobody chose a default: a machine without that folder is an
			// ordinary machine, not a mistake to fix.
			fmt.Fprintf(w, "  - root %s: %s is not there (a default)\n", r.Name, abbreviateHome(r.Path, home))
			continue
		case err != nil:
			report("root %s: %s is not there", r.Name, abbreviateHome(r.Path, home))
			continue
		case !info.IsDir():
			report("root %s: %s is not a directory", r.Name, abbreviateHome(r.Path, home))
			continue
		}
		// One root inside another lists its repositories twice over, and a
		// repository there has two roots to be selected by.
		nested := false
		for j, other := range roots {
			if i != j && repo.Inside(other.Path, r.Path, true) && !repo.SamePath(other.Path, r.Path) {
				report("root %s: %s is inside root %s", r.Name, abbreviateHome(r.Path, home), other.Name)
				nested = true
			}
		}
		switch {
		case nested:
		case isCheckout(r.Path) && !managed(r.Path):
			report("root %s: %s is one repository, and wt does not manage it (wt init there)", r.Name, abbreviateHome(r.Path, home))
		case isCheckout(r.Path):
			fmt.Fprintf(w, "  ✓ root %s: %s (one repository)\n", r.Name, abbreviateHome(r.Path, home))
		default:
			n := len(discoverRepos([]Root{r}).Repos)
			fmt.Fprintf(w, "  ✓ root %s: %s (%s)\n", r.Name, abbreviateHome(r.Path, home), repoCount(n))
		}
	}
	for _, p := range u.Profiles {
		if len(p.Repos) == 0 {
			report("profile %s names no repositories", p.Name)
			continue
		}
		for _, path := range p.Repos {
			t := profileTarget(path, roots)
			switch {
			case t.Problem != "":
				report("profile %s: %s is %s", p.Name, path, t.Problem)
			case t.Root == "":
				report("profile %s: %s is outside every root", p.Name, path)
			default:
				fmt.Fprintf(w, "  ✓ profile %s: %s (in %s)\n", p.Name, abbreviateHome(t.Path, home), t.Root)
			}
		}
	}
}

// DoctorRepositories is doctor from outside every repository: only the roots
// and profiles can be checked there.
func DoctorRepositories(u *config.User, userErr error, w io.Writer) int {
	problems := 0
	report := func(format string, args ...any) {
		problems++
		fmt.Fprintf(w, "  ! "+format+"\n", args...)
	}
	if userErr != nil {
		report("%v", userErr)
	}
	doctorRepos(u, w, report)
	if problems == 0 {
		fmt.Fprintln(w, "No problems found.")
	} else {
		fmt.Fprintf(w, "%d problem(s) found.\n", problems)
	}
	return problems
}
