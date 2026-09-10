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
		got, err := confirmPush(strings.NewReader(tc.in), &out)([]string{"bump"})
		if err != nil || got != tc.want {
			t.Errorf("answer %q: got %v, %v; want %v", tc.in, got, err, tc.want)
		}
		if !strings.Contains(out.String(), "push bump with --force-with-lease? [Y/n]") {
			t.Errorf("question %q", out.String())
		}
	}
	var out bytes.Buffer
	if _, err := confirmPush(strings.NewReader("\n"), &out)([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "push a, b with --force-with-lease? [Y/n]") {
		t.Errorf("question %q", out.String())
	}
}
