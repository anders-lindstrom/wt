// Package jsonout is the vocabulary every --json output shares: how it is
// written, its optional strings, the problems, steps and outcomes it names,
// the journal a run that changes things writes its one object through, and
// the plan token --expect holds a run to.
package jsonout

import (
	"encoding/json"
	"io"
)

// Why wt new or wt checkout would not create a worktree, or what is wrong
// with one it created, as --json names it.
const (
	ProblemBranchExists     = "branchExists"
	ProblemPathExists       = "pathExists"
	ProblemUnknownType      = "unknownType"
	ProblemInvalidName      = "invalidName"
	ProblemNoConfiguration  = "noConfiguration"
	ProblemConfigInvalid    = "configurationInvalid"
	ProblemBranchMissing    = "branchMissing"
	ProblemBranchCheckedOut = "branchCheckedOut"
	ProblemBaseMissing      = "baseMissing"
	// ProblemBranchAmbiguous is a bare name no local branch has and more
	// than one remote does, with no checkout.defaultRemote among them.
	ProblemBranchAmbiguous = "branchAmbiguous"
	// ProblemRemoteBranchMissing is <remote>/<branch> for a configured
	// remote that has no such branch as last fetched.
	ProblemRemoteBranchMissing = "remoteBranchMissing"
	// ProblemPlanChanged is --expect refusing: the plan is not the one the
	// token was made from.
	ProblemPlanChanged = "planChanged"
	// ProblemHeadMoved is a worktree created on another commit than the
	// plan pinned: the branch or base moved between the check and the add.
	ProblemHeadMoved = "headMoved"
)

// The side effects of creating a worktree, in the order they happen.
const (
	StepWorktree   = "worktree"
	StepBranch     = "branch"
	StepConfig     = "config"
	StepCache      = "cache"
	StepProvision  = "provision"
	StepSubmodules = "submodules"
	StepBuild      = "build"
	StepSuperset   = "superset"
)

// What one step came to. Every step ends as exactly one of these.
const (
	StepCreated     = "created"
	StepUntouched   = "untouched"
	StepDone        = "done"
	StepRegistered  = "registered"
	StepSkipped     = "skipped"
	StepFailed      = "failed"
	StepNotRun      = "notRun"
	StepInterrupted = "interrupted"
)

// WriteJSON writes v as indented JSON, the way every --json output is.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// The outcomes a whole run can come to.
const (
	// OutcomeDone is every participant rebased or skipped.
	OutcomeDone = "done"
	// OutcomeRefused is nothing changed: every participant skipped, refused,
	// not run or restored, or the run stopped before choosing any (Error).
	OutcomeRefused = "refused"
	// OutcomePartial is something changed and not everything finished.
	OutcomePartial = "partial"
	// OutcomeInterrupted is a signal ended the run.
	OutcomeInterrupted = "interrupted"
)

// Strp is s as an optional --json string: nil when it is empty.
func Strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Deref is an optional --json string, empty for none.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
