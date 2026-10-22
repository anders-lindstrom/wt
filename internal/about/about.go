// Package about renders what wt says about itself: which build you are running
// and what landed in it. The what's-new text is written by hand and embedded,
// so an old binary still describes itself truthfully.
package about

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/anders-lindstrom/wt/internal/textwrap"
)

//go:embed whats-new.md
var whatsNew string

// newestCount is how many what's-new entries `wt about` shows.
const newestCount = 5

// stampLayout is the local time that opens every heading.
const stampLayout = "2006-01-02 15:04"

// Text is what `wt about` prints, without a trailing newline: the version, how
// this binary was built, and the newest what's-new entries, wrapped to width
// columns (0 for no wrapping, as help does when it is not on a terminal).
func Text(version, buildDate, commitDate string, width int) string {
	return render(version, buildDate, commitDate, whatsNew, width)
}

// NewestHeading names the newest what's-new entry.
func NewestHeading() string {
	entries := sections(whatsNew)
	if len(entries) == 0 {
		return ""
	}
	heading, _, _ := strings.Cut(entries[0], "\n")
	return heading
}

func render(version, buildDate, commitDate, md string, width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wt %s\n", version)
	if line := buildLine(version, buildDate, commitDate); line != "" {
		fmt.Fprintf(&b, "%s\n", line)
	}
	entries := sections(md)
	entries = entries[:min(len(entries), newestCount)]
	for i, entry := range entries {
		if i == 0 {
			b.WriteString("\n")
		} else {
			b.WriteString("\n\n")
		}
		b.WriteString(wrapEntry(entry, width))
	}
	return b.String()
}

// buildLine says when the binary was built, when the commit it was built
// from was made, and whether the tree had uncommitted changes — which
// `git describe --dirty` already records in the version. Several builds a day
// are normal here, so both stamps carry the time. Without a build date there
// is nothing honest to say, so it says nothing: a plain `go build` sets no
// ldflags.
func buildLine(version, buildDate, commitDate string) string {
	if buildDate == "" {
		return ""
	}
	line := "built " + stamp(buildDate)
	if commitDate != "" {
		line += " from a commit of " + stamp(commitDate)
	}
	if strings.HasSuffix(version, "-dirty") {
		line += ", from a modified working tree"
	}
	return line
}

// stamp turns the ldflags-safe 2006-01-02T15:04 into the 2006-01-02 15:04 a
// person reads; a value in another shape is printed as it came.
func stamp(s string) string {
	return strings.Replace(s, "T", " ", 1)
}

// wrapEntry wraps one entry to width columns. A line that starts with "- "
// opens a bullet and the indented lines under it continue it; any other run of
// lines is a paragraph. The heading is one line and is left alone.
func wrapEntry(entry string, width int) string {
	var out, item []string
	prefix := ""
	flush := func() {
		if len(item) > 0 {
			indent := strings.Repeat(" ", len(prefix))
			out = append(out, prefix+textwrap.Wrap(len(prefix), strings.Join(item, " "), width, indent))
			item, prefix = nil, ""
		}
	}
	for n, line := range strings.Split(entry, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case n == 0:
			out = append(out, line)
		case trimmed == "":
			flush()
			out = append(out, "")
		case strings.HasPrefix(line, "- "):
			flush()
			prefix, item = "- ", []string{strings.TrimPrefix(line, "- ")}
		case line[0] == ' ' && len(item) > 0:
			item = append(item, trimmed)
		default:
			if prefix != "" {
				flush()
			}
			item = append(item, trimmed)
		}
	}
	flush()
	return strings.Join(out, "\n")
}

// sections splits the what's-new file into its `## ` entries, newest first,
// with the heading markers dropped: the file is read on GitHub as markdown and
// in a terminal as plain text, and `##` helps only one of those.
func sections(md string) []string {
	const marker = "## "
	var entries []string
	var current []string
	flush := func() {
		if current != nil {
			entries = append(entries, strings.TrimRight(strings.Join(current, "\n"), "\n \t"))
		}
	}
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, marker) {
			flush()
			current = []string{strings.TrimPrefix(line, marker)}
			continue
		}
		if current != nil {
			current = append(current, line)
		}
	}
	flush()
	return entries
}
