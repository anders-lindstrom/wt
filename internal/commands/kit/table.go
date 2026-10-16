package kit

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/github"
)

// ListPadding is the gap tabwriter leaves after every column but the last.
const ListPadding = 2

// minPathWidth keeps a shortened path long enough to still tell worktrees
// apart; a terminal narrower than that wraps the row instead.
const minPathWidth = 24

// PRCell is a pull request in the PR column of wt list and wt status, and on
// the pr line of wt status <work>: prLabel with what its checks say marked
// after the number, as wt pr list says it in words. A pull request with no
// checks has no mark.
func PRCell(pr github.PR, trunks []string) string {
	label := PRLabel(pr, trunks)
	mark := pr.ChecksMark()
	if mark == "" {
		return label
	}
	number, rest, _ := strings.Cut(label, " ")
	return number + " " + mark + " " + rest
}

// PrintPathTable writes rows as aligned columns: the first row is the header
// and the last column of every row is a path. width is the terminal's column
// count, or 0 when output is not a terminal. Above 0, paths are shown from ~
// and shortened from the left so each row fits; at 0 they are printed whole,
// because a printed path is an argument to wt.
func PrintPathTable(w io.Writer, rows [][]string, width int) error {
	if width > 0 {
		FitPaths(rows, width)
	}
	return PrintTable(w, rows)
}

// FitPaths shows the last column of rows, a path, from ~ and shortens it
// from the left so each row fits in width columns, where it can.
func FitPaths(rows [][]string, width int) {
	home, _ := os.UserHomeDir()
	FitLastColumn(rows, width, minPathWidth, func(path string, limit int) string {
		return ElideLeft(AbbreviateHome(path, home), limit)
	})
}

// PrintTable writes rows as aligned columns.
func PrintTable(w io.Writer, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, ListPadding, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

// FitLastColumn shortens the last column of every row with shorten so the
// table fits in width columns, but never below floor. The other columns stay
// whole: they are what a reader types back into wt. A header in the last
// column is shorter than any floor, so shortening leaves it alone.
func FitLastColumn(rows [][]string, width, floor int, shorten func(s string, limit int) string) {
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
		lead += widest + ListPadding
	}
	room := max(width-lead, floor)
	for _, r := range rows {
		r[last] = shorten(r[last], room)
	}
}

// AbbreviateHome writes a path under home as ~/...
func AbbreviateHome(path, home string) string {
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

// ElideLeft shortens path to at most limit runes by dropping leading
// directories, which every worktree of a repository shares, and keeps whole
// trailing ones. A last component longer than limit is cut mid-name.
func ElideLeft(path string, limit int) string {
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
