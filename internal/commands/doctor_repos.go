package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
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

// DoctorAll is wt doctor across repositories: in every one a selection names,
// a few at a time, wt doctor and — where trunk declares wt sync — wt sync
// doctor, each repository a line with what they found under it; then the
// roots and profiles, once. It fails when either found a problem; a sync
// warning is advice, as it is in wt sync doctor.
func DoctorAll(u *config.User, sel Selection, w io.Writer) error {
	set, err := SelectRepos(u, sel)
	if err != nil {
		return err
	}
	type checked struct {
		problems, warnings int
		lines              []string
	}
	results := eachRepo(set.Repos, repoParallelism, func(t RepoTarget) checked {
		if t.Problem != "" {
			return checked{problems: 1, lines: []string{"  ! " + t.Problem}}
		}
		ctx := OpenLenient(t.Path, io.Discard)
		if ctx == nil {
			return checked{problems: 1, lines: []string{"  ! not a git repository"}}
		}
		var c checked
		var buf bytes.Buffer
		n, err := doctor(ctx, &buf, false)
		c.problems = n
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.HasPrefix(l, "  ! ") {
				c.lines = append(c.lines, l)
			}
		}
		if err != nil {
			c.problems++
			c.lines = append(c.lines, "  ! "+err.Error())
		}
		if ctx.ConfigError != nil {
			return c
		}
		if _, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, ctx.Config.MainBranch); err != nil {
			return c
		}
		buf.Reset()
		if err := SyncDoctor(ctx, DoctorOptions{}, &buf); err != nil {
			c.problems++
			c.lines = append(c.lines, "  ! sync: "+oneLine(err.Error()))
		}
		for _, l := range strings.Split(buf.String(), "\n") {
			if f := strings.Fields(l); len(f) >= 2 && f[1] == "warn" {
				c.warnings++
				c.lines = append(c.lines, "  ~ sync "+f[0]+": "+strings.Join(f[2:], " "))
			}
		}
		return c
	})
	home, _ := os.UserHomeDir()
	width := 0
	for _, t := range set.Repos {
		width = max(width, len(t.Name))
	}
	total := 0
	for i, c := range results {
		t := set.Repos[i]
		var verdict []string
		if c.problems > 0 {
			verdict = append(verdict, fmt.Sprintf("%d problem(s)", c.problems))
		}
		if c.warnings > 0 {
			verdict = append(verdict, fmt.Sprintf("%d sync warning(s)", c.warnings))
		}
		if len(verdict) == 0 {
			verdict = []string{"✓"}
		}
		fmt.Fprintf(w, "%-*s  %-26s  %s\n", width, t.Name, strings.Join(verdict, ", "), abbreviateHome(t.Path, home))
		for _, l := range c.lines {
			fmt.Fprintln(w, l)
		}
		total += c.problems
	}
	fmt.Fprintln(w)
	report := func(format string, args ...any) {
		total++
		fmt.Fprintf(w, "  ! "+format+"\n", args...)
	}
	doctorRepos(u, w, report)
	if total > 0 {
		return fmt.Errorf("%d problem(s) found; wt doctor in a repository says more", total)
	}
	fmt.Fprintln(w, "No problems found.")
	return nil
}
