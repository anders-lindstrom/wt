package kit

import (
	"bytes"
	"testing"
)

// Each way a run that asks once can end says what it did not do, in the
// verb's own words; only --yes and a yes go ahead.
func TestDecisionSaysWhyNothingWasDone(t *testing.T) {
	t.Parallel()
	var asked string
	ask := func(answer bool) Ask {
		return func(q string) bool { asked = q; return answer }
	}
	for _, tc := range []struct {
		name  string
		d     Decision
		ahead bool
		out   string
	}{
		{"dry run", Decision{DryRun: true, Ask: ask(true)}, false, "Nothing was swept: --dry-run.\n"},
		{"yes", Decision{Yes: true}, true, ""},
		{"asked yes", Decision{Ask: ask(true)}, true, ""},
		{"asked no", Decision{Ask: ask(false)}, false, "Nothing was swept.\n"},
		{"nobody to ask", Decision{}, false, "Nothing was swept: there is no terminal to ask. Pass --yes to sweep 2 branches.\n"},
	} {
		asked = ""
		var out bytes.Buffer
		if got := tc.d.Decide(&out, "sweep", "swept", "2 branches"); got != tc.ahead || out.String() != tc.out {
			t.Errorf("%s: %v %q, want %v %q", tc.name, got, out.String(), tc.ahead, tc.out)
		}
		if tc.d.Ask != nil && !tc.d.DryRun && asked != "Sweep 2 branches?" {
			t.Errorf("%s: asked %q", tc.name, asked)
		}
	}
}
