// Package about renders what wt says about itself: which build you are running
// and what landed in it. The what's-new text is written by hand and embedded,
// so an old binary still describes itself truthfully.
package about

import (
	_ "embed"
	"fmt"
	"strings"
	"time"
)

//go:embed whats-new.md
var whatsNew string

const (
	// newestCount entries are always shown, however old.
	newestCount = 5
	// recentWindow is how far back an entry still counts as recent; every
	// recent entry is shown, even beyond newestCount.
	recentWindow = 72 * time.Hour
	// stampLayout is the local time that opens every heading.
	stampLayout = "2006-01-02 15:04"
)

// Text is what `wt about` prints, without a trailing newline: the version, how
// this binary was built, and the recent what's-new entries.
func Text(version, buildDate, commitDate string) string {
	return render(version, buildDate, commitDate, whatsNew, time.Now())
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

func render(version, buildDate, commitDate, md string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wt %s\n", version)
	if line := buildLine(version, buildDate, commitDate); line != "" {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if entries := recent(sections(md), now); len(entries) > 0 {
		fmt.Fprintf(&b, "\n%s", strings.Join(entries, "\n\n"))
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

// recent keeps the newest newestCount entries plus every entry stamped within
// recentWindow of now, in file order. An entry without a parseable stamp still
// counts toward the newest, but is never recent.
func recent(entries []string, now time.Time) []string {
	cutoff := now.Add(-recentWindow)
	var out []string
	for i, entry := range entries {
		if i < newestCount {
			out = append(out, entry)
			continue
		}
		if at, ok := headingTime(entry, now.Location()); ok && !at.Before(cutoff) {
			out = append(out, entry)
		}
	}
	return out
}

// headingTime reads the local stamp that opens an entry's heading.
func headingTime(entry string, loc *time.Location) (time.Time, bool) {
	if len(entry) < len(stampLayout) {
		return time.Time{}, false
	}
	at, err := time.ParseInLocation(stampLayout, entry[:len(stampLayout)], loc)
	return at, err == nil
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
