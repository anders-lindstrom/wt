package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/github"
)

// pickTitleWidth truncates a title so the marker beside it still fits.
const pickTitleWidth = 48

// defaultScreen is how many rows to print when the output is not a terminal
// that will say how tall it is.
const defaultScreen = 10

// prChooser builds the picker `wt pr checkout` uses when no number was given.
// It needs no other program on the machine. Everything goes to stderr: stdout
// is the worktree path, so `cd "$(wt pr checkout)"` keeps working.
//
// The rows arrive in the order that matters, this person's review queue first.
// The picker makes a long list answerable: a screenful at a time, any text
// narrows it.
func prChooser(cmd *cobra.Command) func([]commands.PRChoice) (github.PR, error) {
	return func(rows []commands.PRChoice) (github.PR, error) {
		out := cmd.ErrOrStderr()
		ask := canAsk(cmd)
		pk := &picker{out: out, rows: rows, limit: screenful(out)}
		// Nobody to offer the rest to: a script gets the whole list at once,
		// and the number to repeat the command with is in it.
		if !ask {
			pk.limit = 0
		}
		pk.render()
		if !ask {
			return github.PR{}, fmt.Errorf("no terminal to choose in; name the pull request, "+
				"for example: wt pr checkout %d", rows[0].PR.Number)
		}
		p := newPrompter(cmd.InOrStdin(), out)
		for {
			fmt.Fprintf(out, "\nWhich one? [1-%d, #<number>, text to filter; empty to cancel] ", len(pk.rows))
			answer, err := p.line()
			if err != nil {
				return github.PR{}, errCancelled
			}
			chosen, err := pk.answer(answer)
			if err != nil {
				return github.PR{}, err
			}
			if chosen != nil {
				return *chosen, nil
			}
		}
	}
}

// errCancelled ends the command with `wt: cancelled` and a non-zero exit: the
// person answered, and the answer was "not this time". Nothing was made, so
// `cd "$(wt pr checkout)"` must not be handed a path — the exit code is what
// stops it.
var errCancelled = errors.New("cancelled")

// picker is the list being chosen from: every open pull request, or whatever
// a typed filter has narrowed it to, and how much of it fits on a screen.
type picker struct {
	out  io.Writer
	rows []commands.PRChoice
	// limit is how many rows to print; 0 prints them all.
	limit int
	// filter is the text the rows were narrowed by, for the heading.
	filter string
}

// answer reads one reply. A pull request comes back when the reply chose one;
// nil with no error means the list has been redrawn and the question stands.
func (pk *picker) answer(reply string) (*github.PR, error) {
	reply = strings.TrimSpace(reply)
	switch {
	case reply == "":
		return nil, errCancelled
	case strings.EqualFold(reply, "all"), strings.EqualFold(reply, "more"):
		pk.limit = 0
		pk.render()
		return nil, nil
	}
	// A row number as printed, which is what most answers are.
	if n, err := strconv.Atoi(reply); err == nil && n >= 1 && n <= len(pk.rows) {
		return &pk.rows[n-1].PR, nil
	}
	// #12, and bare 12 for anyone who read the #12 and typed the number:
	// only once it cannot be a row number, so a small list still means rows.
	if number, err := strconv.Atoi(strings.TrimPrefix(reply, "#")); err == nil {
		if pr, ok := byNumber(pk.rows, number); ok {
			return pr, nil
		}
		if strings.HasPrefix(reply, "#") {
			fmt.Fprintf(pk.out, "\nNo open pull request #%d in the list.\n", number)
			return nil, nil
		}
	}
	return pk.narrow(reply)
}

// narrow filters the rows by text, matched anywhere in the number, title, head
// branch or author. One match is shown and then chosen, so typing enough of a
// title is a way of picking. Nothing matching leaves the list as it was.
func (pk *picker) narrow(text string) (*github.PR, error) {
	var kept []commands.PRChoice
	for _, r := range pk.rows {
		if matches(r, text) {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		fmt.Fprintf(pk.out, "\nNothing in the list matches %q.\n", text)
		return nil, nil
	}
	pk.rows, pk.filter = kept, text
	if len(kept) == 1 {
		pk.limit = 0
		pk.render()
		return &kept[0].PR, nil
	}
	pk.limit = screenful(pk.out)
	pk.render()
	return nil, nil
}

// matches reports whether a row carries text anywhere a person would look for
// it, case ignored.
func matches(r commands.PRChoice, text string) bool {
	text = strings.ToLower(text)
	for _, field := range []string{
		strconv.Itoa(r.PR.Number), r.PR.Title, r.PR.HeadRefName, r.PR.AuthorLogin(),
	} {
		if strings.Contains(strings.ToLower(field), text) {
			return true
		}
	}
	return false
}

// byNumber finds a pull request in the list by its own number.
func byNumber(rows []commands.PRChoice, number int) (*github.PR, bool) {
	for i := range rows {
		if rows[i].PR.Number == number {
			return &rows[i].PR, true
		}
	}
	return nil, false
}

// render writes the numbered list a person picks from, at most a screenful of
// it, and says how to see the rest.
func (pk *picker) render() {
	heading := "Open pull requests"
	if pk.filter != "" {
		heading = fmt.Sprintf("Open pull requests matching %q", pk.filter)
	}
	fmt.Fprintf(pk.out, "\n%s (%d):\n\n", heading, len(pk.rows))
	shown := pk.rows
	if pk.limit > 0 && len(shown) > pk.limit {
		shown = shown[:pk.limit]
	}
	tw := tabwriter.NewWriter(pk.out, 0, 0, 2, ' ', 0)
	for i, r := range shown {
		fmt.Fprintf(tw, "  %d\t#%d\t%s\t%s\t%s\t%s\t%s\n",
			i+1, r.PR.Number, r.PR.StateLabel(), orDash(r.PR.AuthorLogin()),
			r.PR.HeadRefName, elide(r.PR.Title, pickTitleWidth), marker(r))
	}
	_ = tw.Flush()
	if len(shown) < len(pk.rows) {
		fmt.Fprintf(pk.out, "\n  %d more — `all` shows them, or type part of a title, "+
			"branch or author to narrow the list.\n", len(pk.rows)-len(shown))
	}
}

// marker is what a row says beyond the pull request: that this person has been
// asked to review it, and that its branch already has a worktree here.
func marker(r commands.PRChoice) string {
	var notes []string
	if r.ReviewRequested {
		notes = append(notes, "your review")
	}
	if r.Worktree != "" {
		notes = append(notes, "has a worktree")
	}
	return strings.Join(notes, " · ")
}

// screenful is how many rows to print before offering the rest, leaving room
// for the heading, the note and the prompt. A writer that will not say how
// tall it is gets defaultScreen.
func screenful(w io.Writer) int {
	height := terminalHeight(w)
	if height <= 0 {
		return defaultScreen
	}
	return max(5, height-8)
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
