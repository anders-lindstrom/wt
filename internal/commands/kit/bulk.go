package kit

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/reposel"
)

// Ask puts a yes-or-no question to the person running wt and says what they
// answered. Nil is nobody to ask.
type Ask func(question string) bool

// Confirm asks whether to go ahead with plan, the question built from it.
// Nil is nobody to ask.
type Confirm[P any] func(plan P) bool

// NoRepos is what a run across repositories says when the selection names
// none.
const NoRepos = "No repositories wt manages here; wt repos says where it looked."

// Bulk is a change across repositories, planned in full before any of it is
// made: every repository the selection names is planned, the plans are
// printed, one question covers the lot, and then each repository's plan is
// carried out, one after another.
type Bulk[P any] struct {
	// Watch, when set, handles signals from the first plan to the last
	// apply; it returns the func that stops it.
	Watch func(w io.Writer) func()
	// Plan plans the repositories, in the selection's order. An error ends
	// the run before anything is printed.
	Plan func(repos []reposel.Target) ([]P, error)
	// Report prints the plans and says what carrying them out comes to, "3
	// branches across 2 repositories", or "" when there is nothing to do.
	Report func(w io.Writer, plans []P) (what string)
	// Decide says whether to go ahead with what, and says on w why not when
	// it does not.
	Decide func(w io.Writer, what string) bool
	// Apply carries out the plans.
	Apply func(w io.Writer, plans []P)
	// End is the error the run ends with, at whichever step it stopped.
	End func(plans []P) error
}

// Run runs b across the repositories sel names.
func (b Bulk[P]) Run(u *config.User, sel reposel.Selection, w io.Writer) error {
	set, err := reposel.Select(u, sel)
	if err != nil {
		return err
	}
	if len(set.Repos) == 0 {
		fmt.Fprintln(w, NoRepos)
		return nil
	}
	if b.Watch != nil {
		defer b.Watch(w)()
	}
	plans, err := b.Plan(set.Repos)
	if err != nil {
		return err
	}
	if what := b.Report(w, plans); what != "" && b.Decide(w, what) {
		b.Apply(w, plans)
	}
	return b.End(plans)
}

// Decision is how a run that asks once decides: --dry-run, --yes, or the
// person's answer.
type Decision struct {
	DryRun bool
	Yes    bool
	// Ask puts the question; nil means there is nobody to ask, and nothing
	// goes ahead without Yes.
	Ask Ask
}

// Decide says whether to go ahead with what. verb is what going ahead does,
// "sweep", and done what that did, "swept": a run that does not go ahead says
// on w that nothing was done, and why.
func (d Decision) Decide(w io.Writer, verb, done, what string) bool {
	switch {
	case d.DryRun:
		fmt.Fprintf(w, "Nothing was %s: --dry-run.\n", done)
		return false
	case d.Yes:
		return true
	case d.Ask != nil:
		if d.Ask(strings.ToUpper(verb[:1]) + verb[1:] + " " + what + "?") {
			return true
		}
		fmt.Fprintf(w, "Nothing was %s.\n", done)
		return false
	default:
		fmt.Fprintf(w, "Nothing was %s: there is no terminal to ask. Pass --yes to %s %s.\n", done, verb, what)
		return false
	}
}
