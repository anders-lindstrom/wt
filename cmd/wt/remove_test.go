package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands"
)

// The prompt defaults to no, because the cost of a mistaken yes is a checkout
// and the cost of a mistaken no is retyping the command.
func TestConfirmRemovalAnswers(t *testing.T) {
	for _, tc := range []struct {
		typed string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{" y \n", true},
		{"n\n", false},
		{"\n", false},
		{"anything else\n", false},
		{"", false}, // ^D
	} {
		var out bytes.Buffer
		got, err := confirmRemoval(newPrompter(strings.NewReader(tc.typed), &out))(commands.Plan{})
		if err != nil {
			t.Fatalf("%q: %v", tc.typed, err)
		}
		if got != tc.want {
			t.Errorf("answering %q = %v, want %v", tc.typed, got, tc.want)
		}
		if !strings.Contains(out.String(), "Remove it?") {
			t.Errorf("%q: no prompt was shown", tc.typed)
		}
	}
}
