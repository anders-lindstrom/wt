package kit

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// OneLine flattens a note fragment to fit one line of the overview: an
// embedded newline, carriage return or tab would break the layout of every
// line printed after it.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

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

// ConfigFingerprint is the bytes of the configuration file ctx read, the
// worktree's own or else the main checkout's, the way loadFor finds it.
func ConfigFingerprint(ctx *Context) []byte {
	for _, root := range []string{ctx.Repo.Root, ctx.Repo.MainRoot} {
		for _, name := range []string{"worktree.toml", "worktree.conf"} {
			if data, err := os.ReadFile(filepath.Join(root, "bin", "worktree", name)); err == nil {
				sum := sha256.Sum256(data)
				return sum[:]
			}
		}
	}
	return nil
}
