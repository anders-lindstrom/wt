package about

import (
	"strings"
	"testing"
)

const fixture = `# What's new

## 2026-09-09 — the newest thing

- a line about it.

## 2026-08-01 — the older thing

- a line about that.
`

func TestRenderNamesTheVersionAndTheNewestHeading(t *testing.T) {
	out := render("71ed3e8", "2026-09-09T22:40", "", fixture)
	if !strings.Contains(out, "wt 71ed3e8") {
		t.Errorf("no version line:\n%s", out)
	}
	if !strings.Contains(out, "2026-09-09 — the newest thing") {
		t.Errorf("no newest heading:\n%s", out)
	}
	if strings.Contains(out, "##") {
		t.Errorf("markdown heading markers reached the terminal:\n%s", out)
	}
}

func TestRenderPrintsOnlyTheNewestSection(t *testing.T) {
	out := render("71ed3e8", "2026-09-09T22:40", "", fixture)
	if strings.Contains(out, "the older thing") {
		t.Errorf("older section printed too:\n%s", out)
	}
}

func TestRenderSaysWhenTheBuildCameFromAModifiedTree(t *testing.T) {
	clean := render("71ed3e8", "2026-09-09T22:40", "", fixture)
	if !strings.Contains(clean, "built 2026-09-09 22:40") {
		t.Errorf("no build date:\n%s", clean)
	}
	if strings.Contains(clean, "modified") {
		t.Errorf("a clean build claimed to be modified:\n%s", clean)
	}
	dirty := render("71ed3e8-dirty", "2026-09-09T22:40", "", fixture)
	if !strings.Contains(dirty, "modified working tree") {
		t.Errorf("a -dirty build does not say so:\n%s", dirty)
	}
}

func TestRenderNamesTheCommitTimeWhenKnown(t *testing.T) {
	out := render("71ed3e8", "2026-09-10T06:55", "2026-09-09T23:12", fixture)
	if !strings.Contains(out, "built 2026-09-10 06:55 from a commit of 2026-09-09 23:12") {
		t.Errorf("no commit time, or the T survived:\n%s", out)
	}
}

// A `go build` with no ldflags has no date to print; it should say nothing
// rather than print an empty or bogus one.
func TestRenderOmitsTheBuildLineWhenTheDateIsUnknown(t *testing.T) {
	out := render("dev", "", "", fixture)
	if strings.Contains(out, "built") {
		t.Errorf("build line printed without a date:\n%s", out)
	}
	if !strings.Contains(out, "wt dev") {
		t.Errorf("no version line:\n%s", out)
	}
}

// The shipped file is the source of the printed text; keep it parseable.
func TestTheShippedWhatsNewHasANewestSection(t *testing.T) {
	got := newestSection(whatsNew)
	if got == "" {
		t.Fatalf("no section found in whats-new.md:\n%s", whatsNew)
	}
	if strings.Count(got, "\n") > 8 {
		t.Errorf("the newest section is longer than 8 lines:\n%s", got)
	}
}
