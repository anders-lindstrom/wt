package wtsync

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// messagesPath stands in for a conflict merge-tree reports only in its
// messages, with no three blobs of its own to show.
const messagesPath = "(see messages)"

// Stop is the first commit at which a rebase would stop, with the three
// blobs of every file it conflicts on.
type Stop struct {
	Index     int // 1-based position among the commits the rebase replays
	Total     int
	Commit    string
	Subject   string
	Conflicts []Conflict
	Messages  string
}

// Replay is the outcome of simulating a rebase commit by commit.
type Replay struct {
	Commits int
	Stop    *Stop // nil when every commit replays cleanly
}

// simEnv makes the throwaway commits deterministic and free of any signing
// or identity configuration.
var simEnv = []string{
	"GIT_AUTHOR_NAME=wt sync", "GIT_AUTHOR_EMAIL=wt-sync@localhost",
	"GIT_COMMITTER_NAME=wt sync", "GIT_COMMITTER_EMAIL=wt-sync@localhost",
	"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
}

// SimulateRebase replays branch onto `onto` inside the object store, the way
// `git rebase onto` would, and reports the first commit that conflicts. No
// ref, index or working tree is touched; the only objects created are
// unreachable blobs, trees and commits.
func SimulateRebase(mainRoot, onto, branch string) (Replay, error) {
	// The same selection and order the rebase sequencer uses: right side
	// only, patch-equivalent commits dropped, merges flattened, topological.
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--reverse", "--topo-order", "--right-only", "--cherry-pick", "--no-merges", onto+"..."+branch, "--")
	if err != nil {
		return Replay{}, err
	}
	var commits []string
	if out != "" {
		commits = strings.Split(out, "\n")
	}
	base, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", onto+"^{commit}", "--")
	if err != nil {
		return Replay{}, err
	}
	for i, c := range commits {
		tree, clean, conflicts, messages, err := mergeTree(mainRoot, c+"^", base, c)
		if err != nil {
			return Replay{}, err
		}
		if !clean {
			subject, err := gitEnv(mainRoot, nil, nil, "log", "-1", "--format=%s", c, "--")
			if err != nil {
				return Replay{}, err
			}
			return Replay{Commits: len(commits), Stop: &Stop{
				Index: i + 1, Total: len(commits), Commit: c, Subject: subject, Conflicts: conflicts, Messages: messages,
			}}, nil
		}
		// A commit whose changes are already present replays to the same
		// tree; rebase drops it, so no simulated commit is made for it.
		// base is always a SHA computed above (verified, or a commit-tree
		// result), never a user-supplied ref, so it carries no path
		// ambiguity; no "--" here, since plain (non-`--verify`) `rev-parse`
		// echoes a trailing "--" back as a second output line, which would
		// corrupt this comparison.
		baseTree, err := gitEnv(mainRoot, nil, nil, "rev-parse", base+"^{tree}")
		if err != nil {
			return Replay{}, err
		}
		if baseTree == tree {
			continue
		}
		base, err = gitEnv(mainRoot, simEnv, nil, "commit-tree", tree, "-p", base, "-m", "wt sync simulation")
		if err != nil {
			return Replay{}, err
		}
	}
	return Replay{Commits: len(commits)}, nil
}

// Endpoint merges the two tips and returns the conflicts, nil when clean. A
// conflict merge-tree reports only in its messages (no three blobs) comes
// back as one Conflict with Incomplete set and the message as its path
// description.
func Endpoint(mainRoot, onto, branch string) ([]Conflict, error) {
	_, clean, conflicts, messages, err := mergeTree(mainRoot, "", onto, branch)
	if err != nil {
		return nil, err
	}
	if clean {
		return nil, nil
	}
	if len(conflicts) == 0 && messages != "" {
		conflicts = []Conflict{{Path: messagesPath, Incomplete: messages}}
	}
	return conflicts, nil
}

// BehindAhead counts the commits branch lacks from onto, and onto from branch.
func BehindAhead(mainRoot, onto, branch string) (behind, ahead int, err error) {
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--left-right", "--count", onto+"..."+branch, "--")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", out)
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return behind, ahead, nil
}

// mergeTree runs one merge in the object store. mergeBase may be "" to let
// git find it. clean reports whether the merge succeeded outright, taken
// straight from merge-tree's exit status. On conflict it returns every
// conflicted file with its blobs (a missing stage or a non-blob entry marks
// the conflict Incomplete) and merge-tree's informational messages.
func mergeTree(mainRoot, mergeBase, onto, commit string) (tree string, clean bool, conflicts []Conflict, messages string, err error) {
	args := []string{"merge-tree", "--write-tree", "-z"}
	if mergeBase != "" {
		args = append(args, "--merge-base="+mergeBase)
	}
	args = append(args, "--", onto, commit)
	cmd := exec.Command("git", args...)
	cmd.Dir = mainRoot
	out, runErr := cmd.Output()
	if runErr != nil {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || exit.ExitCode() != 1 {
			return "", false, nil, "", fmt.Errorf("git merge-tree: %v: %s", runErr, stderrOf(runErr))
		}
	}
	// With -z the output is NUL-separated: the tree, then for a conflict one
	// record per index entry ("<mode> <oid> <stage>\t<path>"), then an empty
	// record ending the section, then the informational messages.
	records := strings.Split(string(out), "\x00")
	tree = strings.TrimSpace(records[0])
	if runErr == nil {
		return tree, true, nil, "", nil
	}
	stages := map[string]*Conflict{}
	seenStage := map[string]map[int]bool{}
	var order []string
	i := 1
	for ; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			break
		}
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			continue
		}
		stage, _ := strconv.Atoi(fields[2])
		c, seen := stages[path]
		if !seen {
			c = &Conflict{Path: path}
			stages[path] = c
			seenStage[path] = map[int]bool{}
			order = append(order, path)
		}
		seenStage[path][stage] = true
		if fields[0] != "100644" && fields[0] != "100755" {
			c.Incomplete = "not a regular file (mode " + fields[0] + ")"
			continue
		}
		data, err := catFileRaw(mainRoot, fields[1])
		if err != nil {
			return "", false, nil, "", err
		}
		switch stage {
		case 1:
			c.Base = data
		case 2:
			c.Trunk = data
		case 3:
			c.Branch = data
		}
	}
	if i+1 < len(records) {
		messages = parseMessages(records[i+1:])
	}
	for _, p := range order {
		c := stages[p]
		if c.Incomplete == "" && (!seenStage[p][1] || !seenStage[p][2] || !seenStage[p][3]) {
			switch {
			case !seenStage[p][1] && seenStage[p][2] && seenStage[p][3]:
				c.Incomplete = "both sides added it"
			default:
				c.Incomplete = "one side deleted or renamed it"
			}
		}
		conflicts = append(conflicts, *c)
	}
	return tree, false, conflicts, messages, nil
}

// parseMessages extracts the human-readable text from merge-tree's
// informational section: NUL-separated groups of a path count, that many
// paths, a conflict-type label, and the message itself
// (e.g. "1\x00v.txt\x00CONFLICT (contents)\x00CONFLICT (content): ...\n").
// Only the message of each group is kept, one per line.
func parseMessages(records []string) string {
	var msgs []string
	i := 0
	for i < len(records) {
		n, err := strconv.Atoi(records[i])
		if err != nil {
			break
		}
		i++
		i += n // the group's paths, not needed: Conflicts already names them
		if i >= len(records) {
			break
		}
		i++ // the conflict-type label, redundant with the message
		if i >= len(records) {
			break
		}
		if msg := strings.TrimSpace(records[i]); msg != "" {
			msgs = append(msgs, msg)
		}
		i++
	}
	return strings.Join(msgs, "\n")
}

// catFileRaw reads a blob without trimming.
func catFileRaw(mainRoot, oid string) ([]byte, error) {
	cmd := exec.Command("git", "cat-file", "blob", oid)
	cmd.Dir = mainRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file blob %s: %v: %s", oid, err, stderrOf(err))
	}
	return out, nil
}

func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return strings.TrimSpace(string(exit.Stderr))
	}
	return ""
}
