package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/github"
)

func prs() []github.PR {
	return []github.PR{
		{Number: 206, Title: "Arch", HeadRefName: "feat_wt/arch", State: "OPEN"},
		{Number: 12, Title: "Fixes", HeadRefName: "residential_fixes", State: "OPEN"},
	}
}

// The list is numbered 1..n and the pull requests carry their own numbers, so
// both readings of a typed number have to work — and an empty line is a
// refusal, not a default.
func TestChoosePR(t *testing.T) {
	for answer, want := range map[string]int{
		"1":    206,
		"2":    12,
		"#12":  12,
		"#206": 206,
		" 2 ":  12,
		// 12 is a row that does not exist, so it is read as the number.
		"12": 12,
	} {
		t.Run(answer, func(t *testing.T) {
			pr, err := choosePR(prs(), answer)
			if err != nil {
				t.Fatal(err)
			}
			if pr.Number != want {
				t.Errorf("choosePR(%q) = #%d, want #%d", answer, pr.Number, want)
			}
		})
	}
}

func TestChoosePRRefusals(t *testing.T) {
	if _, err := choosePR(prs(), ""); !errors.Is(err, errCancelled) {
		t.Errorf("an empty answer gave %v, want a cancel", err)
	}
	if _, err := choosePR(prs(), "   "); !errors.Is(err, errCancelled) {
		t.Errorf("a blank answer gave %v, want a cancel", err)
	}
	for _, answer := range []string{"nope", "#999", "99", "#nope", "0"} {
		if _, err := choosePR(prs(), answer); err == nil {
			t.Errorf("choosePR(%q) chose something", answer)
		}
	}
}

// The list goes to stderr, because stdout is the worktree path.
func TestPrintPRs(t *testing.T) {
	var out bytes.Buffer
	printPRs(&out, prs())
	got := out.String()
	for _, want := range []string{"  1  #206  open", "  2  #12   open", "residential_fixes", "Fixes"} {
		if !strings.Contains(got, want) {
			t.Errorf("printPRs wrote\n%s\nwant %q", got, want)
		}
	}
}
