// Package textwrap breaks prose at spaces for a terminal of a given width.
package textwrap

import (
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// Width is the number of terminal columns s takes. A wide or fullwidth East
// Asian character, most emoji among them, counts two; a combining mark or a
// zero-width character such as a joiner counts none; anything else counts
// one. An emoji sequence held together by joiners counts each of its parts,
// so it can come out wider than a terminal draws it.
func Width(s string) int {
	n := 0
	for _, r := range s {
		if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
			continue
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			n += 2
		default:
			n++
		}
	}
	return n
}

// Wrap breaks s at spaces so no line is wider than width columns, continuing
// under indent. The first line starts at column start, and is returned without
// whatever stands to its left. Width 0 or less leaves s on one line. A word
// wider than the width stays whole.
func Wrap(start int, s string, width int, indent string) string {
	if width <= 0 {
		return s
	}
	var lines []string
	line, col := "", start
	gapBroken := false
	brk := func(word string) {
		lines = append(lines, strings.TrimRight(line, " "))
		line = indent + word
		col = Width(line)
	}
	// Split on single spaces, so a run of them (the gap between a table's
	// columns) survives as it was written; a line never starts with one.
	for _, word := range strings.Split(s, " ") {
		n := Width(word)
		switch {
		case word == "" && line == "":
			continue
		case line == "":
			line, col = word, col+n
		case word == "" && gapBroken:
			// The rest of a gap the line broke inside: nothing to keep.
		case word == "" && col+1 > width:
			gapBroken = true
		case gapBroken:
			gapBroken = false
			brk(word)
		case col+1+n <= width:
			line, col = line+" "+word, col+1+n
		default:
			brk(word)
		}
	}
	return strings.Join(append(lines, strings.TrimRight(line, " ")), "\n")
}
