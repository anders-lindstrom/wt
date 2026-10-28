package kit

import "testing"

func TestCountSaysTheNounInTheNumberItIs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		n    int
		many []string
		want string
	}{
		{0, nil, "0 commits"},
		{1, nil, "1 commit"},
		{2, nil, "2 commits"},
		{1, []string{"commitz"}, "1 commit"},
		{3, []string{"commitz"}, "3 commitz"},
	} {
		if got := Count(tc.n, "commit", tc.many...); got != tc.want {
			t.Errorf("Count(%d, commit, %v) = %q, want %q", tc.n, tc.many, got, tc.want)
		}
	}
}
