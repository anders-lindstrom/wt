// Package about renders what wt says about itself: which build you are running
// and what landed in it. The what's-new text is written by hand and embedded,
// so an old binary still describes itself truthfully.
package about

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed whats-new.md
var whatsNew string

// Text is what `wt about` prints, without a trailing newline: the version, how
// this binary was built, and the newest what's-new entry.
func Text(version, buildDate string) string {
	return render(version, buildDate, whatsNew)
}

// NewestHeading names the newest what's-new entry.
func NewestHeading() string {
	heading, _, _ := strings.Cut(newestSection(whatsNew), "\n")
	return heading
}

func render(version, buildDate, md string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wt %s\n", version)
	if line := buildLine(version, buildDate); line != "" {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if section := newestSection(md); section != "" {
		fmt.Fprintf(&b, "\n%s", section)
	}
	return b.String()
}

// buildLine says when the binary was built, and whether the tree it was built
// from had uncommitted changes — which `git describe --dirty` already records
// in the version. Without a date there is nothing honest to say, so it says
// nothing: a plain `go build` sets no ldflags.
func buildLine(version, buildDate string) string {
	if buildDate == "" {
		return ""
	}
	line := "built " + buildDate
	if strings.HasSuffix(version, "-dirty") {
		line += ", from a modified working tree"
	}
	return line
}

// newestSection returns the first `## ` section of the what's-new file, with
// the heading markers dropped: the file is read on GitHub as markdown and in a
// terminal as plain text, and `##` helps only one of those.
func newestSection(md string) string {
	const marker = "## "
	var out []string
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, marker) {
			if len(out) > 0 {
				break
			}
			out = append(out, strings.TrimPrefix(line, marker))
			continue
		}
		if len(out) > 0 {
			out = append(out, line)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n \t")
}
