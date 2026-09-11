package about

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const fixture = `# What's new

## 2026-09-09 22:00 — the newest thing

- a line about it.

## 2026-08-01 09:00 — the older thing

- a line about that.
`

// now is a fixed "today" for every test; stamps are read in its location.
var now = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// whatsNewOf builds a what's-new file with one entry per stamp, newest first,
// each headed "<stamp> — entry <n>".
func whatsNewOf(stamps ...string) string {
	var b strings.Builder
	b.WriteString("# What's new\n\nIntro.\n")
	for i, s := range stamps {
		fmt.Fprintf(&b, "\n## %s — entry %d\n\n- body of entry %d.\n", s, i, i)
	}
	return b.String()
}

func headings(entries []string) []string {
	var out []string
	for _, e := range entries {
		h, _, _ := strings.Cut(e, "\n")
		out = append(out, h)
	}
	return out
}

// ago stamps now minus d in the heading layout.
func ago(d time.Duration) string {
	return now.Add(-d).Format(stampLayout)
}

func TestRenderNamesTheVersionAndTheNewestHeading(t *testing.T) {
	out := render("71ed3e8", "2026-09-09T22:40", "", fixture, now)
	if !strings.Contains(out, "wt 71ed3e8") {
		t.Errorf("no version line:\n%s", out)
	}
	if !strings.Contains(out, "2026-09-09 22:00 — the newest thing") {
		t.Errorf("no newest heading:\n%s", out)
	}
	if strings.Contains(out, "##") {
		t.Errorf("markdown heading markers reached the terminal:\n%s", out)
	}
}

func TestRenderSeparatesEntriesWithOneBlankLine(t *testing.T) {
	out := render("71ed3e8", "2026-09-09T22:40", "", fixture, now)
	want := "built 2026-09-09 22:40\n\n" +
		"2026-09-09 22:00 — the newest thing\n\n- a line about it.\n\n" +
		"2026-08-01 09:00 — the older thing\n\n- a line about that."
	if !strings.HasSuffix(out, want) {
		t.Errorf("render =\n%s\nwant it to end with\n%s", out, want)
	}
}

func TestMoreThanFiveOldEntriesShowsExactlyFive(t *testing.T) {
	var stamps []string
	for i := 0; i < 8; i++ {
		stamps = append(stamps, ago(10*24*time.Hour+time.Duration(i)*time.Hour))
	}
	got := headings(recent(sections(whatsNewOf(stamps...)), now))
	if len(got) != 5 {
		t.Fatalf("got %d entries, want 5: %q", len(got), got)
	}
	if !strings.HasSuffix(got[0], "entry 0") || !strings.HasSuffix(got[4], "entry 4") {
		t.Errorf("not the newest five, in order: %q", got)
	}
}

func TestEveryEntryWithinThreeDaysIsShownBeyondFive(t *testing.T) {
	var stamps []string
	for i := 0; i < 20; i++ {
		stamps = append(stamps, ago(time.Duration(i)*time.Hour))
	}
	stamps = append(stamps, ago(4*24*time.Hour), ago(5*24*time.Hour))
	got := headings(recent(sections(whatsNewOf(stamps...)), now))
	if len(got) != 20 {
		t.Fatalf("got %d entries, want the 20 recent ones: %q", len(got), got)
	}
}

func TestFewerThanFiveEntriesShowsThemAll(t *testing.T) {
	md := whatsNewOf(ago(time.Hour), ago(30*24*time.Hour), ago(400*24*time.Hour))
	got := headings(recent(sections(md), now))
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %q", len(got), got)
	}
}

// An entry stamped exactly 72 hours ago is still recent; a minute older is not.
func TestTheThreeDayWindowIncludesItsBoundary(t *testing.T) {
	stamps := []string{ago(0), ago(time.Hour), ago(2 * time.Hour), ago(3 * time.Hour),
		ago(4 * time.Hour), ago(72 * time.Hour), ago(72*time.Hour + time.Minute)}
	got := headings(recent(sections(whatsNewOf(stamps...)), now))
	if len(got) != 6 {
		t.Fatalf("got %d entries, want 6 (boundary in, a minute past out): %q", len(got), got)
	}
	if !strings.HasSuffix(got[5], "entry 5") {
		t.Errorf("the 72-hour entry is missing: %q", got)
	}
}

// A heading without a stamp takes one of the newest five, and is never recent
// enough to be shown past them.
func TestAnUnparseableHeadingCountsTowardFiveButIsNeverRecent(t *testing.T) {
	stamps := []string{"no stamp here", ago(time.Hour), ago(2 * time.Hour),
		ago(10 * 24 * time.Hour), ago(11 * 24 * time.Hour), ago(12 * 24 * time.Hour)}
	got := headings(recent(sections(whatsNewOf(stamps...)), now))
	if len(got) != 5 || got[0] != "no stamp here — entry 0" {
		t.Errorf("unstamped newest entry should take a slot of five: %q", got)
	}

	var old []string
	for i := 0; i < 5; i++ {
		old = append(old, ago(10*24*time.Hour))
	}
	md := whatsNewOf(append(old, "2026-09-11 — a date without a time")...)
	got = headings(recent(sections(md), now))
	if len(got) != 5 {
		t.Errorf("an unparseable sixth entry was shown as recent: %q", got)
	}
}

func TestRenderSaysWhenTheBuildCameFromAModifiedTree(t *testing.T) {
	clean := render("71ed3e8", "2026-09-09T22:40", "", fixture, now)
	if !strings.Contains(clean, "built 2026-09-09 22:40") {
		t.Errorf("no build date:\n%s", clean)
	}
	if strings.Contains(clean, "modified") {
		t.Errorf("a clean build claimed to be modified:\n%s", clean)
	}
	dirty := render("71ed3e8-dirty", "2026-09-09T22:40", "", fixture, now)
	if !strings.Contains(dirty, "modified working tree") {
		t.Errorf("a -dirty build does not say so:\n%s", dirty)
	}
}

func TestRenderNamesTheCommitTimeWhenKnown(t *testing.T) {
	out := render("71ed3e8", "2026-09-10T06:55", "2026-09-09T23:12", fixture, now)
	if !strings.Contains(out, "built 2026-09-10 06:55 from a commit of 2026-09-09 23:12") {
		t.Errorf("no commit time, or the T survived:\n%s", out)
	}
}

// A `go build` with no ldflags has no date to print; it should say nothing
// rather than print an empty or bogus one.
func TestRenderOmitsTheBuildLineWhenTheDateIsUnknown(t *testing.T) {
	out := render("dev", "", "", fixture, now)
	if strings.Contains(out, "built") {
		t.Errorf("build line printed without a date:\n%s", out)
	}
	if !strings.Contains(out, "wt dev") {
		t.Errorf("no version line:\n%s", out)
	}
}

// The shipped file is the source of the printed text; keep it parseable, and
// keep every entry stamped so the three-day rule can see it.
func TestTheShippedWhatsNewIsStampedAndShort(t *testing.T) {
	entries := sections(whatsNew)
	if len(entries) == 0 {
		t.Fatalf("no section found in whats-new.md:\n%s", whatsNew)
	}
	if strings.Count(entries[0], "\n") > 8 {
		t.Errorf("the newest section is longer than 8 lines:\n%s", entries[0])
	}
	for _, e := range entries {
		if _, ok := headingTime(e, time.Local); !ok {
			h, _, _ := strings.Cut(e, "\n")
			t.Errorf("heading has no YYYY-MM-DD HH:MM stamp: %q", h)
		}
	}
}
