package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/github"
)

// prChooser builds the picker `wt pr checkout` uses when no number was given.
// It needs no other program on the machine. Everything goes to stderr:
// stdout is the worktree path, so `cd "$(wt pr checkout)"` keeps working.
func prChooser(cmd *cobra.Command) func([]github.PR) (github.PR, error) {
	return func(prs []github.PR) (github.PR, error) {
		out := cmd.ErrOrStderr()
		printPRs(out, prs)
		// Without a terminal there is nobody to answer, and guessing would
		// check out a pull request the caller did not name. The list has
		// already been printed, so the number to repeat with is on screen.
		if !canAsk(cmd) {
			return github.PR{}, errors.New("no terminal to choose in; name the pull request, " +
				"for example: wt pr checkout " + strconv.Itoa(prs[0].Number))
		}
		p := newPrompter(cmd.InOrStdin(), out)
		fmt.Fprintf(out, "\nWhich one? [1-%d, or #<number>; empty to cancel] ", len(prs))
		answer, err := p.line()
		if err != nil {
			return github.PR{}, errCancelled
		}
		return choosePR(prs, answer)
	}
}

// errCancelled ends the command without a complaint: the person answered, and
// the answer was "not this time".
var errCancelled = errors.New("cancelled")

// choosePR reads the answer: a row number as printed, or #<number> — or the
// bare pull request number, for anyone who read the #12 and typed 12.
func choosePR(prs []github.PR, answer string) (github.PR, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return github.PR{}, errCancelled
	}
	if number, ok := strings.CutPrefix(answer, "#"); ok {
		return prByNumber(prs, number)
	}
	n, err := strconv.Atoi(answer)
	if err != nil {
		return github.PR{}, fmt.Errorf("%q is not one of 1-%d", answer, len(prs))
	}
	if n >= 1 && n <= len(prs) {
		return prs[n-1], nil
	}
	return prByNumber(prs, answer)
}

func prByNumber(prs []github.PR, number string) (github.PR, error) {
	n, err := strconv.Atoi(number)
	if err != nil {
		return github.PR{}, fmt.Errorf("%q is not a pull request number", number)
	}
	for _, pr := range prs {
		if pr.Number == n {
			return pr, nil
		}
	}
	return github.PR{}, fmt.Errorf("no open pull request #%d in the list above", n)
}

// printPRs writes the numbered list a person picks from, and the same list a
// script gets told to read when there is no terminal to pick in.
func printPRs(w io.Writer, prs []github.PR) {
	fmt.Fprintf(w, "Open pull requests:\n\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, pr := range prs {
		fmt.Fprintf(tw, "  %d\t#%d\t%s\t%s\t%s\t%s\n",
			i+1, pr.Number, pr.StateLabel(), orDash(pr.AuthorLogin()),
			pr.HeadRefName, elide(pr.Title, 48))
	}
	_ = tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// elide shortens a title to at most limit runes.
func elide(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}
