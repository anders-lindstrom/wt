package git

import (
	"errors"
	"fmt"
	"testing"
)

func TestReasonPicksTheLineWorthShowing(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		skip []string
		want string
	}{
		{
			name: "git's advice to run something else is skipped",
			err:  errors.New("fatal: cannot remove a locked working tree, lock reason: moving\nuse 'remove -f -f' to override or unlock first"),
			skip: []string{"use '", "hint:"},
			want: "cannot remove a locked working tree, lock reason: moving",
		},
		{
			name: "ssh's warning before the reason is skipped whatever its case",
			err: errors.New("Warning: Identity file /tmp/key not accessible: No such file or directory.\n" +
				"ssh: Could not resolve hostname github.com: nodename nor servname provided\n" +
				"fatal: Could not read from remote repository."),
			skip: []string{"warning:", "hint:"},
			want: "ssh: Could not resolve hostname github.com: nodename nor servname provided",
		},
		{
			name: "words a caller wrapped around git's stay",
			err:  fmt.Errorf("%w (uncommitted changes? try removing it by hand)", errors.New("fatal: 'x' contains modified or untracked files, use --force to delete it")),
			skip: []string{"use '", "hint:"},
			want: "'x' contains modified or untracked files, use --force to delete it (uncommitted changes? try removing it by hand)",
		},
		{
			name: "nothing worth showing comes back on one line",
			err:  errors.New("hint: one\nhint:  two"),
			skip: []string{"hint:"},
			want: "hint: one hint: two",
		},
	} {
		if got := Reason(c.err, c.skip...); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
