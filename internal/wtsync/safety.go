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

// ResultPrefix is where a run pins the tip a branch ended at, once its
// rebase and its deferred steps are done. Undo compares the branch against
// it: a branch that has moved on since the run is work the run did not
// make, and resetting to the safety ref would throw it away. A restored or
// failed rebase writes none, so "no result ref" means the run never
// finished for that branch.
const ResultPrefix = "refs/wt-sync-result/"

// ForwardPrefix is where a run pins the commit it fast-forwards a branch to
// before rebasing it: a commit of the branch's own remote, which the branch
// held only because the run put it there. Undo reads it as where the run has
// left the branch so far, and it is what tells a tip the branch had by
// itself from one a run gave it: once the run is undone or its rebase put
// back, that commit was never the branch's.
const ForwardPrefix = "refs/wt-sync-ff/"

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
	s := Safety{Branch: branch, Epoch: epoch, Tip: tip, Ref: SafetyRef(branch, epoch)}
	// The zero-oid old value makes update-ref fail if the ref already exists.
	if _, err := gitEnv(mainRoot, nil, nil, "update-ref", s.Ref, tip, "0000000000000000000000000000000000000000"); err != nil {
		return Safety{}, fmt.Errorf("safety ref %s: %w", s.Ref, err)
	}
	return s, nil
}

// SafetyRef names the safety ref of one branch in one run. A run can say
// where the old tip will be before it has pinned it there.
func SafetyRef(branch string, epoch int64) string {
	return SafetyPrefix + branch + "/" + strconv.FormatInt(epoch, 10)
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

// resultRef names the result ref of one branch in one run.
func resultRef(branch string, epoch int64) string {
	return ResultPrefix + branch + "/" + strconv.FormatInt(epoch, 10)
}

// WriteResult pins where branch ended up in the run epoch. Unlike a safety
// ref it overwrites: the run owns this ref for the whole of its own epoch.
func WriteResult(mainRoot, branch, tip string, epoch int64) error {
	ref := resultRef(branch, epoch)
	if _, err := gitEnv(mainRoot, nil, nil, "update-ref", ref, tip); err != nil {
		return fmt.Errorf("result ref %s: %w", ref, err)
	}
	return nil
}

// ResultTip reads the result ref of one branch in one run. for-each-ref
// exits zero with empty output for a ref that is not there, so an absent
// ref is not confused with a git failure.
func ResultTip(mainRoot, branch string, epoch int64) (string, bool, error) {
	out, err := gitEnv(mainRoot, nil, nil, "for-each-ref", "--format=%(objectname)", resultRef(branch, epoch))
	if err != nil {
		return "", false, err
	}
	if out == "" {
		return "", false, nil
	}
	return out, true, nil
}

// Pin is one branch's refs for a run: where the branch was before it
// (Safety), where the run left it (Result) and the commit of its own remote
// the run fast-forwards it to first (Forward). Result and Forward are left
// out when empty.
type Pin struct{ Branch, Safety, Result, Forward string }

// WriteRun writes every pin's safety and result ref for one epoch in a
// single update-ref transaction, so a failure part way through cannot leave
// a stray safety ref pinning a tip nothing will ever restore. create
// refuses a ref that already exists, the same guard WriteSafety makes on
// its own.
func WriteRun(mainRoot string, epoch int64, pins []Pin) error {
	if len(pins) == 0 {
		return nil
	}
	var b strings.Builder
	for _, p := range pins {
		fmt.Fprintf(&b, "create %s %s\n", SafetyRef(p.Branch, epoch), p.Safety)
		if p.Result != "" {
			fmt.Fprintf(&b, "create %s %s\n", resultRef(p.Branch, epoch), p.Result)
		}
		if p.Forward != "" {
			fmt.Fprintf(&b, "create %s %s\n", forwardRef(p.Branch, epoch), p.Forward)
		}
	}
	if _, err := gitEnv(mainRoot, nil, strings.NewReader(b.String()), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("safety refs for run %d: %w", epoch, err)
	}
	return nil
}

// DeleteSafety drops one safety ref, and the result and fast-forward refs of
// the same run when there are any: they pin history the same run superseded.
func DeleteSafety(mainRoot string, s Safety) error {
	if _, err := gitEnv(mainRoot, nil, nil, "update-ref", "-d", s.Ref, s.Tip); err != nil {
		return err
	}
	if err := dropRef(mainRoot, forwardRef(s.Branch, s.Epoch)); err != nil {
		return err
	}
	tip, ok, err := ResultTip(mainRoot, s.Branch, s.Epoch)
	if err != nil || !ok {
		return err
	}
	_, err = gitEnv(mainRoot, nil, nil, "update-ref", "-d", resultRef(s.Branch, s.Epoch), tip)
	return err
}

// forwardRef names the fast-forward ref of one branch in one run.
func forwardRef(branch string, epoch int64) string {
	return ForwardPrefix + branch + "/" + strconv.FormatInt(epoch, 10)
}

// ForwardTip reads the commit a run fast-forwarded branch to, when it did.
func ForwardTip(mainRoot, branch string, epoch int64) (string, bool, error) {
	return refTip(mainRoot, forwardRef(branch, epoch))
}

// refTip reads one ref by its exact name; an absent one is not an error.
func refTip(mainRoot, ref string) (string, bool, error) {
	out, err := gitEnv(mainRoot, nil, nil, "for-each-ref", "--format=%(refname) %(objectname)", ref)
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if name, tip, _ := strings.Cut(line, " "); name == ref {
			return tip, true, nil
		}
	}
	return "", false, nil
}

// dropRef deletes one ref when it is there.
func dropRef(mainRoot, ref string) error {
	tip, ok, err := refTip(mainRoot, ref)
	if err != nil || !ok {
		return err
	}
	_, err = gitEnv(mainRoot, nil, nil, "update-ref", "-d", ref, tip)
	return err
}

// Disown takes back what a run's fast-forward left behind, once the branch
// is no longer where that fast-forward put it: undone, or put back with the
// rebase that followed. The commit was the remote's and the branch held it
// only because the run moved it there, so nothing may go on saying the
// branch once had it. The fast-forward ref goes; the result ref goes when it
// is that same commit, which is a run that only fast-forwarded; and the
// entries the fast-forward wrote in the branch's reflog go, so that git's
// own --force-if-includes refuses to push over the commit as well. Nothing
// local is lost: the commit is on the remote. A run that fast-forwarded
// nothing is left as it is.
func Disown(mainRoot, branch string, epoch int64) error {
	tip, ok, err := ForwardTip(mainRoot, branch, epoch)
	if err != nil || !ok {
		return err
	}
	if err := dropReflog(mainRoot, branch, tip, epoch); err != nil {
		return err
	}
	if result, ok, err := ResultTip(mainRoot, branch, epoch); err != nil {
		return err
	} else if ok && result == tip {
		if err := dropRef(mainRoot, resultRef(branch, epoch)); err != nil {
			return err
		}
	}
	return dropRef(mainRoot, forwardRef(branch, epoch))
}

// dropReflog removes the entries of branch's reflog that are at commit and
// were written by the run of epoch or after it: an older entry at the same
// commit is the person's own and stays. Each entry after a removed one has
// its old value rewritten, so the log still reads as a chain. A branch with
// no reflog has nothing to remove.
func dropReflog(mainRoot, branch, commit string, epoch int64) error {
	ref := "refs/heads/" + branch
	out, err := gitEnv(mainRoot, nil, nil, "reflog", "show", "--format=%H %gd", "--date=unix", ref, "--")
	if err != nil {
		return nil
	}
	lines := strings.Split(out, "\n")
	// From the oldest up: removing an entry renumbers only the ones older
	// than it, which are already done.
	for i := len(lines) - 1; i >= 0; i-- {
		sha, selector, _ := strings.Cut(lines[i], " ")
		open := strings.LastIndex(selector, "@{")
		if sha != commit || open < 0 {
			continue
		}
		when, perr := strconv.ParseInt(strings.TrimSuffix(selector[open+2:], "}"), 10, 64)
		if perr != nil || when < epoch/int64(time.Second) {
			continue
		}
		if _, err := gitEnv(mainRoot, nil, nil, "reflog", "delete", "--rewrite", ref+"@{"+strconv.Itoa(i)+"}"); err != nil {
			return fmt.Errorf("reflog of %s: %w", branch, err)
		}
	}
	return nil
}
