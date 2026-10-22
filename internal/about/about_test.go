package about

import (
	"fmt"
	"regexp"
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
	out := render("71ed3e8", "2026-09-09T22:40", "", fixture, 0)
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
	out := render("71ed3e8", "2026-09-09T22:40", "", fixture, 0)
	want := "built 2026-09-09 22:40\n\n" +
		"2026-09-09 22:00 — the newest thing\n\n- a line about it.\n\n" +
		"2026-08-01 09:00 — the older thing\n\n- a line about that."
	if !strings.HasSuffix(out, want) {
		t.Errorf("render =\n%s\nwant it to end with\n%s", out, want)
	}
}

func TestMoreThanFiveEntriesShowsTheNewestFive(t *testing.T) {
	var stamps []string
	for i := 0; i < 8; i++ {
		stamps = append(stamps, ago(time.Duration(i)*time.Hour))
	}
	out := render("v", "", "", whatsNewOf(stamps...), 0)
	if n := strings.Count(out, "— entry "); n != 5 {
		t.Fatalf("got %d entries, want 5:\n%s", n, out)
	}
	if !strings.Contains(out, "entry 4") || strings.Contains(out, "entry 5") {
		t.Errorf("not the newest five:\n%s", out)
	}
}

func TestFewerThanFiveEntriesShowsThemAll(t *testing.T) {
	md := whatsNewOf(ago(time.Hour), ago(30*24*time.Hour), ago(400*24*time.Hour))
	if got := headings(sections(md)); len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %q", len(got), got)
	}
}

// about wraps like help: to the width it is given, bullets hanging under
// their text, and not at all for width 0.
func TestEntriesWrapLikeHelp(t *testing.T) {
	md := "## 2026-09-09 22:00 — heading\n\nA paragraph that goes on\nover two lines.\n\n" +
		"- a bullet that is long\n  and continues here.\n- second.\n"
	wide := render("v", "", "", md, 0)
	for _, want := range []string{"A paragraph that goes on over two lines.", "- a bullet that is long and continues here.\n- second."} {
		if !strings.Contains(wide, want) {
			t.Errorf("unwrapped about lacks %q:\n%s", want, wide)
		}
	}
	narrow := render("v", "", "", md, 20)
	if !strings.Contains(narrow, "- a bullet that is\n  long and continues\n  here.\n- second.") {
		t.Errorf("bullet did not hang under its text:\n%s", narrow)
	}
}

func TestRenderSaysWhenTheBuildCameFromAModifiedTree(t *testing.T) {
	clean := render("71ed3e8", "2026-09-09T22:40", "", fixture, 0)
	if !strings.Contains(clean, "built 2026-09-09 22:40") {
		t.Errorf("no build date:\n%s", clean)
	}
	if strings.Contains(clean, "modified") {
		t.Errorf("a clean build claimed to be modified:\n%s", clean)
	}
	dirty := render("71ed3e8-dirty", "2026-09-09T22:40", "", fixture, 0)
	if !strings.Contains(dirty, "modified working tree") {
		t.Errorf("a -dirty build does not say so:\n%s", dirty)
	}
}

func TestRenderNamesTheCommitTimeWhenKnown(t *testing.T) {
	out := render("71ed3e8", "2026-09-10T06:55", "2026-09-09T23:12", fixture, 0)
	if !strings.Contains(out, "built 2026-09-10 06:55 from a commit of 2026-09-09 23:12") {
		t.Errorf("no commit time, or the T survived:\n%s", out)
	}
}

// A `go build` with no ldflags has no date to print; it should say nothing
// rather than print an empty or bogus one.
func TestRenderOmitsTheBuildLineWhenTheDateIsUnknown(t *testing.T) {
	out := render("dev", "", "", fixture, 0)
	if strings.Contains(out, "built") {
		t.Errorf("build line printed without a date:\n%s", out)
	}
	if !strings.Contains(out, "wt dev") {
		t.Errorf("no version line:\n%s", out)
	}
}

// The shipped file is the source of the printed text: every entry has a
// heading that parses, and they run newest first.
func TestShippedWhatsNewHeadingsParseInOrder(t *testing.T) {
	stamps := regexp.MustCompile(`## [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}`)
	entries := sections(whatsNew)
	if len(entries) == 0 {
		t.Fatalf("no section found in whats-new.md:\n%s", whatsNew)
	}
	if parsed, stamped := len(entries), len(stamps.FindAllString(whatsNew, -1)); parsed != stamped {
		t.Errorf("parsed %d entries but found %d stamped headings", parsed, stamped)
	}
	var previous time.Time
	for _, entry := range entries {
		stamp, err := time.ParseInLocation(stampLayout, entry[:min(len(entry), len(stampLayout))], time.Local)
		if err != nil {
			t.Errorf("invalid heading: %q", entry)
			continue
		}
		if !previous.IsZero() && stamp.After(previous) {
			t.Errorf("%s comes after older entry %s", stamp, previous)
		}
		previous = stamp
	}
}
