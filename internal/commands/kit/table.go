package kit

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/github"
)

// ListPadding is the gap tabwriter leaves after every column but the last.
const ListPadding = 2

// minPathWidth keeps a shortened path long enough to still tell worktrees
// apart; a terminal narrower than that wraps the row instead.
const minPathWidth = 24

// PRCell is a pull request in the PR column of wt list and wt status, and on
// the pr line of wt status <work>: PRLabel with what its checks say marked
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

// PathTableFits reports whether PrintPathTable would print every row of rows
// within width terminal columns, paths shortened as it shortens them. rows
// is not changed.
func PathTableFits(rows [][]string, width int) bool {
	fitted := make([][]string, len(rows))
	for i, r := range rows {
		fitted[i] = slices.Clone(r)
	}
	FitPaths(fitted, width)
	last := len(fitted[0]) - 1
	lead := 0
	for col := range last {
		widest := 0
		for _, r := range fitted {
			widest = max(widest, utf8.RuneCountInString(r[col]))
		}
		lead += widest + ListPadding
	}
	for _, r := range fitted {
		// Columns are padded by character count, so a cell with characters
		// two columns wide pushes the rest of its row out by the difference.
		wider := 0
		for _, cell := range r[:last] {
			wider += DisplayWidth(cell) - utf8.RuneCountInString(cell)
		}
		if lead+wider+DisplayWidth(r[last]) > width {
			return false
		}
	}
	return true
}

// DisplayWidth is how many terminal columns s takes: two for the East Asian
// wide and fullwidth characters and for emoji, none for a combining mark.
func DisplayWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	switch {
	case unicode.In(r, unicode.Mn, unicode.Me):
		return 0
	case r >= 0x1100 && r <= 0x115f, // Hangul Jamo
		r >= 0x2e80 && r <= 0xa4cf && r != 0x303f,              // CJK, kana, Yi
		r >= 0xac00 && r <= 0xd7a3,                             // Hangul syllables
		r >= 0xf900 && r <= 0xfaff,                             // CJK compatibility ideographs
		r >= 0xfe30 && r <= 0xfe6f,                             // CJK compatibility forms
		r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6, // fullwidth forms
		r >= 0x1f300 && r <= 0x1faff, // emoji
		r >= 0x20000 && r <= 0x3fffd: // CJK extensions
		return 2
	}
	return 1
}

// CutToWidth is s in at most limit terminal columns, ending in … when it
// was cut.
func CutToWidth(s string, limit int) string {
	if DisplayWidth(s) <= limit {
		return s
	}
	n := 0
	for i, r := range s {
		if n += runeWidth(r); n > limit-1 {
			return s[:i] + "…"
		}
	}
	return s
}

// Dash is s, or - for an empty cell.
func Dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
