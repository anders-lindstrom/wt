package repo

import (
	"fmt"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// PushTo is a branch on a remote: where a branch pushes when its own name
// does not say. wt records one per branch as branch.<name>.wtPushTo,
// "<remote> <branch>", in the branch's own section, so a rename carries it
// and deleting the branch takes it away.
type PushTo struct{ Remote, Branch string }

// String is the destination as a person says it, origin/feat/x.
func (p PushTo) String() string { return p.Remote + "/" + p.Branch }

// PushToVar is the variable's name in a branch's section.
const PushToVar = "wtPushTo"

func pushToKey(branch string) string { return "branch." + branch + "." + PushToVar }

// ParsePushTo reads a recorded value.
func ParsePushTo(value string) (PushTo, bool) {
	remote, branch, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || remote == "" || branch == "" || strings.Contains(branch, " ") {
		return PushTo{}, false
	}
	return PushTo{Remote: remote, Branch: branch}, true
}

// PushTo is the destination recorded for branch; ok is false when there is
// none. A value that cannot be read is an error.
func (r *Repo) PushTo(branch string) (to PushTo, ok bool, err error) {
	out, err := git.Exec(git.Opts{Dir: r.MainRoot}, "config", "--get", pushToKey(branch))
	if code, aerr := git.Answer(err, 1); aerr != nil {
		return PushTo{}, false, aerr
	} else if code == 1 {
		return PushTo{}, false, nil
	}
	value := strings.TrimSpace(string(out))
	if to, ok = ParsePushTo(value); !ok {
		return PushTo{}, false, fmt.Errorf("%s is %q, not a remote and a branch", pushToKey(branch), value)
	}
	return to, true, nil
}

// SetPushTo records where branch pushes.
func (r *Repo) SetPushTo(branch string, to PushTo) error {
	_, err := git.Run(r.MainRoot, "config", "--local", pushToKey(branch), to.Remote+" "+to.Branch)
	return err
}

// UnsetPushTo forgets where branch pushes. Nothing recorded is not an error.
func (r *Repo) UnsetPushTo(branch string) error {
	_, err := git.Exec(git.Opts{Dir: r.MainRoot}, "config", "--local", "--unset-all", pushToKey(branch))
	_, err = git.Answer(err, 5)
	return err
}

// PushToName reports whether name can be the branch a push writes: a name
// git takes for a branch, as written, and none that only reads like a branch
// in a refspec: @ is HEAD, a leading + forces, refs/... is a ref's full name
// where the branch's is wanted. Asked when a destination is recorded, and
// again each time a recorded one is read, since the record is a line in a
// file anybody can edit.
func (r *Repo) PushToName(name string) bool {
	if name == "@" || strings.HasPrefix(name, "+") || strings.HasPrefix(name, "refs/") {
		return false
	}
	out, err := git.Run(r.MainRoot, "check-ref-format", "--branch", name)
	return err == nil && out == name
}
