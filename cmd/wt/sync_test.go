package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirmPushDefaultsToYes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"\n", true},
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"q\n", false},
		{"", false}, // ^D declines
	} {
		var out bytes.Buffer
		got, err := confirmPush(newPrompter(strings.NewReader(tc.in), &out))([]string{"bump"})
		if err != nil || got != tc.want {
			t.Errorf("answer %q: got %v, %v; want %v", tc.in, got, err, tc.want)
		}
		if !strings.Contains(out.String(), "push bump with --force-with-lease? [Y/n]") {
			t.Errorf("question %q", out.String())
		}
	}
	var out bytes.Buffer
	if _, err := confirmPush(newPrompter(strings.NewReader("\n"), &out))([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "push a, b with --force-with-lease? [Y/n]") {
		t.Errorf("question %q", out.String())
	}
}

func TestConfirmAskNamesTheWorktreesAndDefaultsToNo(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"\n", false},
		{"y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"", false}, // ^D declines
	} {
		var out bytes.Buffer
		got, err := confirmAsk(newPrompter(strings.NewReader(tc.in), &out), "rebase")([]string{"a", "b"})
		if err != nil || got != tc.want {
			t.Errorf("answer %q: got %v, %v; want %v", tc.in, got, err, tc.want)
		}
		if !strings.Contains(out.String(), "rebase a, b? [y/N]") {
			t.Errorf("question %q", out.String())
		}
	}
}

// sync run asks before the rebase and again before the push. Both answers can
// be typed ahead, so both questions have to read through one prompter.
func TestRunQuestionsShareOneReader(t *testing.T) {
	var out bytes.Buffer
	p := newPrompter(strings.NewReader("n\ny\n"), &out)
	if ok, _ := confirmAsk(p, "rebase")([]string{"bump"}); ok {
		t.Error("the rebase question did not read its n")
	}
	if ok, _ := confirmPush(p)([]string{"bump"}); !ok {
		t.Error("the push question lost the y typed ahead for it")
	}
	if want := "rebase bump? [y/N] push bump with --force-with-lease? [Y/n] "; out.String() != want {
		t.Errorf("questions = %q, want %q", out.String(), want)
	}
}
