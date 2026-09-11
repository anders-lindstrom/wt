package commands

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// listPadding is the gap tabwriter leaves after every column but the last.
const listPadding = 2

// minPathWidth keeps a shortened path long enough to still tell worktrees
// apart; a terminal narrower than that wraps the row instead.
const minPathWidth = 24

// List prints every worktree of the repository, in whatever layout it is in.
// Anything not at the canonical path is marked, and the two marks mean
// different things: "s" is Superset's layout, which is deliberate and must be
// left alone, while "!" is a layout nothing owns and `wt migrate` can move.
func List(ctx *Context, w io.Writer, width int) error {
	names, err := WorkNames(ctx)
	if err != nil {
		return err
	}
	rows := [][]string{{"", "WORK", "BRANCH", "PATH"}}
	var seen [3]bool
	for _, n := range names {
		work, branch := "(main)", n.Branch
		if branch == "" {
			branch = "(detached)"
		}
		mark := ""
		if !n.IsMain {
			layout := naming.Foreign
			if n.Work != "" {
				work = n.Work
				layout = naming.Classify(n.Path, ctx.Repo.Parent, ctx.Repo.Name,
					n.Type, n.Work, ctx.Config.TypeSuffix)
			} else {
				work = "-"
			}
			seen[layout] = true
			mark = layoutMark(layout)
		}
		rows = append(rows, []string{mark, work, branch, n.Path})
	}
	if err := printPathTable(w, rows, width); err != nil {
		return err
	}
	if seen[naming.Superset] || seen[naming.Foreign] {
		fmt.Fprintln(w, "")
	}
	if seen[naming.Superset] {
		fmt.Fprintln(w, "s  Superset's layout — its workspace holds this path; leave it where it is")
	}
	if seen[naming.Foreign] {
		fmt.Fprintln(w, "!  not a layout wt recognises — `wt migrate <work|branch|path>` moves it to the")
		fmt.Fprintln(w, "   canonical path; add a destination to rename or retype it as it goes")
	}
	return nil
}

func layoutMark(l naming.Layout) string {
	switch l {
	case naming.Superset:
		return "s"
	case naming.Foreign:
		return "!"
	default:
		return ""
	}
}

// Status prints each worktree's branch and whether its checkout is clean.
func Status(ctx *Context, w io.Writer, width int) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	rows := [][]string{{"BRANCH", "STATE", "PATH"}}
	for _, wt := range worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		state := "clean"
		if dirty, err := repo.Dirty(wt.Path, false); err != nil {
			state = "unreadable"
		} else if dirty {
			state = "dirty"
		}
		rows = append(rows, []string{branch, state, wt.Path})
	}
	return printPathTable(w, rows, width)
}

// printPathTable writes rows as aligned columns: the first row is the header
// and the last column of every row is a path. width is the terminal's column
// count, or 0 when output is not a terminal. Above 0, paths are shown from ~
// and shortened from the left so each row fits; at 0 they are printed whole,
// because a printed path is an argument to wt.
func printPathTable(w io.Writer, rows [][]string, width int) error {
	if width > 0 {
		home, _ := os.UserHomeDir()
		fitLastColumn(rows, width, minPathWidth, func(path string, limit int) string {
			return elideLeft(abbreviateHome(path, home), limit)
		})
	}
	return printTable(w, rows)
}

// printTable writes rows as aligned columns.
func printTable(w io.Writer, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, listPadding, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

// fitLastColumn shortens the last column of every row with shorten so the
// table fits in width columns, but never below floor. The other columns stay
// whole: they are what a reader types back into wt. A header in the last
// column is shorter than any floor, so shortening leaves it alone.
func fitLastColumn(rows [][]string, width, floor int, shorten func(s string, limit int) string) {
	if len(rows) == 0 {
		return
	}
	last := len(rows[0]) - 1
	lead := 0
	for col := range last {
		widest := 0
		for _, r := range rows {
			widest = max(widest, utf8.RuneCountInString(r[col]))
		}
		lead += widest + listPadding
	}
	room := max(width-lead, floor)
	for _, r := range rows {
		r[last] = shorten(r[last], room)
	}
}

func abbreviateHome(path, home string) string {
	if home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// elideLeft shortens path to at most limit runes by dropping leading
// directories, which every worktree of a repository shares, and keeps whole
// trailing ones. A last component longer than limit is cut mid-name.
func elideLeft(path string, limit int) string {
	if utf8.RuneCountInString(path) <= limit {
		return path
	}
	for rest := path; ; {
		_, after, ok := strings.Cut(rest, "/")
		if !ok {
			break
		}
		rest = after
		if utf8.RuneCountInString(rest)+2 <= limit {
			return "…/" + rest
		}
	}
	r := []rune(path)
	return "…" + string(r[len(r)-(limit-1):])
}
