package commands

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
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
//
// width is the terminal's column count, or 0 when output is not a terminal.
// Above 0, paths are shown from ~ and shortened from the left so each row fits;
// at 0 they are printed whole, because a printed path is an argument to wt.
func List(ctx *Context, w io.Writer, width int) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	rows := [][4]string{{"", "WORK", "BRANCH", "PATH"}}
	var seen [3]bool
	for _, wt := range worktrees {
		work, branch := "(main)", wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		mark := ""
		if !wt.IsMain {
			layout := naming.Foreign
			if typ, name, ok := naming.ParseBranch(wt.Branch, ctx.Config.TypeSuffix); ok {
				work = name
				layout = naming.Classify(wt.Path, ctx.Repo.Parent, ctx.Repo.Name,
					typ, name, ctx.Config.TypeSuffix)
			} else {
				work = "-"
			}
			seen[layout] = true
			mark = layoutMark(layout)
		}
		rows = append(rows, [4]string{mark, work, branch, wt.Path})
	}
	if width > 0 {
		fitPaths(rows, width)
	}
	tw := tabwriter.NewWriter(w, 0, 0, listPadding, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r[0], r[1], r[2], r[3])
	}
	if err := tw.Flush(); err != nil {
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

// fitPaths shortens the PATH cell of every row after the header so the table
// fits in width columns. The other columns stay whole: they are what a reader
// types back into wt.
func fitPaths(rows [][4]string, width int) {
	lead := 0
	for col := range 3 {
		widest := 0
		for _, r := range rows {
			widest = max(widest, utf8.RuneCountInString(r[col]))
		}
		lead += widest + listPadding
	}
	room := max(width-lead, minPathWidth)
	home, _ := os.UserHomeDir()
	for i := 1; i < len(rows); i++ {
		rows[i][3] = elideLeft(abbreviateHome(rows[i][3], home), room)
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
func Status(ctx *Context, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tSTATE\tPATH")
	for _, wt := range worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		state := "clean"
		if out, err := git.Run(wt.Path, "status", "--porcelain"); err != nil {
			state = "unreadable"
		} else if out != "" {
			state = "dirty"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", branch, state, wt.Path)
	}
	return tw.Flush()
}
