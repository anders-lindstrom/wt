package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/github"
)

func rows() []commands.PRChoice {
	return []commands.PRChoice{
		{PR: github.PR{Number: 206, Title: "Arch", HeadRefName: "feat_wt/arch", State: "OPEN"},
			ReviewRequested: true},
		{PR: github.PR{Number: 12, Title: "Residents keep their doors", HeadRefName: "residential_fixes",
			State: "OPEN", Author: github.Account{Login: "someone"}}},
		{PR: github.PR{Number: 40, Title: "Draft of the thing", HeadRefName: "feat_wt/thing",
			State: "OPEN", IsDraft: true}, Worktree: "/src/demo_wt/feat_wt/thing"},
	}
}

func newPicker(out *bytes.Buffer) *picker {
	return &picker{out: out, rows: rows(), limit: defaultScreen}
}

// The list is numbered 1..n and the pull requests carry their own numbers, so
// both readings of a typed number have to work.
func TestPickerAnswersANumber(t *testing.T) {
	for answer, want := range map[string]int{
		"1":   206,
		"2":   12,
		"#12": 12,
		"#40": 40,
		" 2 ": 12,
		// 12 is a row that does not exist, so it is read as the number.
		"12": 12,
	} {
		t.Run(answer, func(t *testing.T) {
			var out bytes.Buffer
			pr, err := newPicker(&out).answer(answer)
			if err != nil {
				t.Fatal(err)
			}
			if pr == nil || pr.Number != want {
				t.Errorf("answer(%q) = %v, want #%d", answer, pr, want)
			}
		})
	}
}

func TestPickerCancels(t *testing.T) {
	for _, answer := range []string{"", "   "} {
		var out bytes.Buffer
		if _, err := newPicker(&out).answer(answer); !errors.Is(err, errCancelled) {
			t.Errorf("answer(%q) gave %v, want a cancel", answer, err)
		}
	}
}

// A number nobody has and text nobody matches both leave the question
// standing, rather than ending the command: the person is at a prompt and can
// try again.
func TestPickerKeepsAskingAfterAMiss(t *testing.T) {
	for answer, want := range map[string]string{
		"#999":    "No open pull request #999 in the list.",
		"bananas": `Nothing in the list matches "bananas".`,
	} {
		t.Run(answer, func(t *testing.T) {
			var out bytes.Buffer
			pk := newPicker(&out)
			pr, err := pk.answer(answer)
			if err != nil || pr != nil {
				t.Fatalf("answer(%q) = %v, %v; want the question to stand", answer, pr, err)
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("said %q, want %q", out.String(), want)
			}
			if len(pk.rows) != 3 {
				t.Errorf("the list was narrowed to %d rows", len(pk.rows))
			}
		})
	}
}

// Typed text narrows the list, and the narrowed list is what the next answer
// is read against.
func TestPickerNarrows(t *testing.T) {
	var out bytes.Buffer
	pk := newPicker(&out)
	pr, err := pk.answer("feat_wt")
	if err != nil || pr != nil {
		t.Fatalf("answer = %v, %v", pr, err)
	}
	if len(pk.rows) != 2 {
		t.Fatalf("narrowed to %d rows, want 2", len(pk.rows))
	}
	if !strings.Contains(out.String(), `Open pull requests matching "feat_wt" (2)`) {
		t.Errorf("the heading does not say what it matched:\n%s", out.String())
	}
	if strings.Contains(out.String(), "residential_fixes") {
		t.Errorf("a row that does not match was still printed:\n%s", out.String())
	}
	// Row 2 of the narrowed list, not of the original one.
	again, err := pk.answer("2")
	if err != nil || again == nil || again.Number != 40 {
		t.Errorf("answer(2) = %v, %v; want #40", again, err)
	}
}

// Enough text to leave one row is a way of picking: it is shown, then taken.
func TestPickerTakesASingleMatchAfterShowingIt(t *testing.T) {
	var out bytes.Buffer
	pr, err := newPicker(&out).answer("Residents")
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil || pr.Number != 12 {
		t.Fatalf("answer = %v, want #12", pr)
	}
	if !strings.Contains(out.String(), "#12") {
		t.Errorf("the chosen row was not shown:\n%s", out.String())
	}
}

// Matching is on everything a person would look for, case ignored.
func TestPickerMatchesEverythingOnARow(t *testing.T) {
	for _, text := range []string{"RESIDENTS", "residential", "someone"} {
		var out bytes.Buffer
		pk := newPicker(&out)
		if _, err := pk.answer(text); err != nil {
			t.Fatal(err)
		}
		if len(pk.rows) != 1 || pk.rows[0].PR.Number != 12 {
			t.Errorf("%q narrowed to %+v", text, pk.rows)
		}
	}
	// Part of a pull request's number, once it can be neither a row nor a
	// pull request of its own.
	var out bytes.Buffer
	pr, err := newPicker(&out).answer("4")
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil || pr.Number != 40 {
		t.Errorf("answer(4) = %v, want #40", pr)
	}
}

// A long list is a screenful with a way to the rest, because a picker you
// have to scroll is one you cannot answer.
func TestPickerShowsAScreenfulAndThenTheRest(t *testing.T) {
	var many []commands.PRChoice
	for i := 1; i <= 25; i++ {
		many = append(many, commands.PRChoice{PR: github.PR{
			Number: i, Title: fmt.Sprintf("Work %d", i), HeadRefName: fmt.Sprintf("b%d", i), State: "OPEN"}})
	}
	var out bytes.Buffer
	pk := &picker{out: &out, rows: many, limit: 10}
	pk.render()
	if strings.Contains(out.String(), "Work 11") {
		t.Errorf("more than a screenful was printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "15 more — `all` shows them") {
		t.Errorf("no way to the rest:\n%s", out.String())
	}

	out.Reset()
	if _, err := pk.answer("all"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Work 25") {
		t.Errorf("`all` did not show the rest:\n%s", out.String())
	}
	if strings.Contains(out.String(), "more — `all`") {
		t.Errorf("`all` still offered more:\n%s", out.String())
	}
}

// What a row says about itself beyond the pull request: a draft is a draft, a
// review waiting on you says so, and a branch already in a worktree here is
// marked so you do not make a second one.
func TestPickerRowsCarryTheirMarks(t *testing.T) {
	var out bytes.Buffer
	newPicker(&out).render()
	got := out.String()
	for _, want := range []string{
		"  1  #206  open", "your review",
		"  2  #12   open", "someone", "residential_fixes",
		"  3  #40   draft", "has a worktree",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render wrote\n%s\nwant %q", got, want)
		}
	}
}
