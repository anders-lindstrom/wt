package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands"
)

var detected = commands.Answers{MainBranch: "main", BranchPrefix: "feat_wt"}

func TestAskAnswersKeepsTheOfferedDefaultOnAnEmptyLine(t *testing.T) {
	var out bytes.Buffer
	got, err := askAnswers(strings.NewReader("\n\n\n"), &out)(detected)
	if err != nil {
		t.Fatalf("askAnswers: %v", err)
	}
	if got != detected {
		t.Errorf("answers = %+v, want the offered defaults %+v", got, detected)
	}
	if !strings.Contains(out.String(), "[main]") || !strings.Contains(out.String(), "[feat_wt]") {
		t.Errorf("the prompts do not show the defaults they would keep:\n%s", out.String())
	}
}

func TestAskAnswersReadsAllThreePromptsFromOneStream(t *testing.T) {
	var out bytes.Buffer
	got, err := askAnswers(strings.NewReader("trunk\nchore_wt\nmake build\n"), &out)(detected)
	if err != nil {
		t.Fatalf("askAnswers: %v", err)
	}
	want := commands.Answers{MainBranch: "trunk", BranchPrefix: "chore_wt", BuildCommand: "make build"}
	if got != want {
		t.Errorf("answers = %+v, want %+v", got, want)
	}
}

// Ctrl-D at any prompt aborts, rather than silently accepting the rest of the
// defaults as though they had been confirmed.
func TestAskAnswersAbortsOnEndOfInput(t *testing.T) {
	var out bytes.Buffer
	if _, err := askAnswers(strings.NewReader("trunk\n"), &out)(detected); err == nil {
		t.Fatal("askAnswers accepted a truncated set of answers")
	}
}

func TestRootListsInit(t *testing.T) {
	out, err := runCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out, "init") {
		t.Errorf("--help does not mention init:\n%s", out)
	}
}

// Init re-asks after a rejected answer, so the prompt function is called more
// than once against one stream. A reader built per call would buffer past its
// own line and drop the answers meant for the second round.
func TestAskAnswersSurvivesBeingCalledAgainForACorrection(t *testing.T) {
	var out bytes.Buffer
	ask := askAnswers(strings.NewReader("main\nwip_wt\n\nmain\nchore_wt\n\n"), &out)

	first, err := ask(detected)
	if err != nil {
		t.Fatalf("first ask: %v", err)
	}
	if first.BranchPrefix != "wip_wt" {
		t.Fatalf("first BranchPrefix = %q, want %q", first.BranchPrefix, "wip_wt")
	}

	second, err := ask(first)
	if err != nil {
		t.Fatalf("second ask: %v", err)
	}
	if second.BranchPrefix != "chore_wt" {
		t.Errorf("second BranchPrefix = %q, want the corrected %q", second.BranchPrefix, "chore_wt")
	}
}
