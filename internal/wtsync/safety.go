package wtsync

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SafetyPrefix is where a run pins the tip it is about to rewrite. A plain
// ref outside refs/heads: the branch namespace is contended in a repository
// with many worktrees, a branch cannot be checked out twice, and a ref here
// is invisible to --update-refs (spec §4).
const SafetyPrefix = "refs/wt-sync/"

// Safety is one pinned tip: the branch it belonged to, the run that pinned it
// (Epoch, nanoseconds; every ref of one run shares it), and where.
type Safety struct {
	Branch string
	Epoch  int64
	Ref    string
	Tip    string
}

// WriteSafety pins tip as refs/wt-sync/<branch>/<epoch>. It refuses to move
// an existing ref of the same name: two runs in one second would otherwise
// silently share one undo point.
func WriteSafety(mainRoot, branch, tip string, epoch int64) (Safety, error) {
	s := Safety{Branch: branch, Epoch: epoch, Tip: tip, Ref: SafetyPrefix + branch + "/" + strconv.FormatInt(epoch, 10)}
	// The zero-oid old value makes update-ref fail if the ref already exists.
	if _, err := gitEnv(mainRoot, nil, nil, "update-ref", s.Ref, tip, "0000000000000000000000000000000000000000"); err != nil {
		return Safety{}, fmt.Errorf("safety ref %s: %w", s.Ref, err)
	}
	return s, nil
}

// ListSafety returns every safety ref, newest epoch first.
func ListSafety(mainRoot string) ([]Safety, error) {
	out, err := gitEnv(mainRoot, nil, nil, "for-each-ref", "--format=%(refname) %(objectname)", SafetyPrefix)
	if err != nil {
		return nil, err
	}
	var list []Safety
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		ref, tip, _ := strings.Cut(line, " ")
		rest := strings.TrimPrefix(ref, SafetyPrefix)
		i := strings.LastIndex(rest, "/")
		if i < 0 {
			continue // not ours to interpret
		}
		epoch, err := strconv.ParseInt(rest[i+1:], 10, 64)
		if err != nil {
			continue
		}
		list = append(list, Safety{Branch: rest[:i], Epoch: epoch, Ref: ref, Tip: tip})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Epoch > list[j].Epoch })
	return list, nil
}

// LatestSafety is the newest safety ref for branch.
func LatestSafety(mainRoot, branch string) (Safety, bool, error) {
	all, err := ListSafety(mainRoot)
	if err != nil {
		return Safety{}, false, err
	}
	for _, s := range all {
		if s.Branch == branch {
			return s, true, nil
		}
	}
	return Safety{}, false, nil
}

// Prunable applies the retention rule: keep anything younger than keep, and
// keep every run group (all refs sharing an epoch) that is the newest run of
// any branch, so what undo would restore stays whole; everything else pins
// abandoned history and blocks garbage collection (spec §4).
func Prunable(all []Safety, now time.Time, keep time.Duration) []Safety {
	newest := map[string]int64{}
	for _, s := range all {
		if s.Epoch > newest[s.Branch] {
			newest[s.Branch] = s.Epoch
		}
	}
	keepEpoch := map[int64]bool{}
	for _, e := range newest {
		keepEpoch[e] = true
	}
	var out []Safety
	for _, s := range all {
		if keepEpoch[s.Epoch] {
			continue
		}
		if now.Sub(time.Unix(0, s.Epoch)) < keep {
			continue
		}
		out = append(out, s)
	}
	return out
}

// DeleteSafety drops one safety ref.
func DeleteSafety(mainRoot string, s Safety) error {
	_, err := gitEnv(mainRoot, nil, nil, "update-ref", "-d", s.Ref, s.Tip)
	return err
}
