package syncrun

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/wtsync"
)

// StepOptions is what a sync verb is told about the steps a declaration
// runs: whether to go on when a step's need does not answer, and the probe
// of each need.
type StepOptions struct {
	// AllowOwed starts a run whose steps need something that did not answer
	// its probe: those steps are not run, and are owed.
	AllowOwed bool
	// Probes replaces the probe of each need; nil probes for real.
	Probes map[string]func() error
}

// CheckFailed is an each_commit check that failed, for --json.
type CheckFailed struct {
	Step    string `json:"step"`
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
	Error   string `json:"error"`
}

// DeferredOwed did not run because a need of it did not answer; owed.
const DeferredOwed = "owed"

// Prober probes each need once for a verb, however many worktrees ask.
type Prober struct {
	probes  map[string]func() error
	answers map[string]error
}

// Prober is a fresh prober over o's probes, or the real ones.
func (o StepOptions) Prober() *Prober {
	probes := o.Probes
	if probes == nil {
		probes = wtsync.KnownNeeds
	}
	return &Prober{probes: probes, answers: map[string]error{}}
}

// Probe is need's answer, asked the first time and remembered after.
func (p *Prober) Probe(need string) error {
	if err, ok := p.answers[need]; ok {
		return err
	}
	err := wtsync.ProbeNeedsWith(p.probes, []string{need})[need]
	p.answers[need] = err
	return err
}

// NeedsFor probes the needs of what may run for one worktree. unavailable
// is each need that did not answer, with its answer; refusal is the line a
// run refuses the worktree with, "" when nothing it needs is missing or
// allowOwed lets it go on without.
func NeedsFor(p *Prober, due wtsync.Due, allowOwed bool) (unavailable map[string]string, refusal string) {
	for _, n := range due.Needs() {
		err := p.Probe(n)
		if err == nil {
			continue
		}
		if allowOwed {
			if unavailable == nil {
				unavailable = map[string]string{}
			}
			unavailable[n] = kit.OneLine(err.Error())
			continue
		}
		steps := due.Needing(n)
		for i, s := range steps {
			steps[i] = "`" + s + "`"
		}
		return nil, fmt.Sprintf("%s needs %s, which did not answer: %s; start it, or rebase now with --allow-owed and run the step by hand later",
			strings.Join(steps, ", "), n, kit.OneLine(err.Error()))
	}
	return unavailable, ""
}

// AllowedOwedLine is the line a run prints for a worktree it rebases with
// steps owed for a need that did not answer; "" when there are none.
func AllowedOwedLine(unavailable map[string]string) string {
	if len(unavailable) == 0 {
		return ""
	}
	needs := make([]string, 0, len(unavailable))
	for n := range unavailable {
		needs = append(needs, n)
	}
	sort.Strings(needs)
	for i, n := range needs {
		needs[i] = n + " (" + unavailable[n] + ")"
	}
	return "⚠ --allow-owed: " + strings.Join(needs, ", ") + " did not answer; the steps that need it are owed"
}

// CheckFailedOf is f for --json; nil for none.
func CheckFailedOf(f *wtsync.CheckFailure) *CheckFailed {
	if f == nil {
		return nil
	}
	return &CheckFailed{Step: f.Step, Commit: f.Commit, Subject: f.Subject, Error: kit.OneLine(f.Err)}
}

// OwedChecks is the each_commit checks a rebase did not run, named as the
// owed steps are.
func OwedChecks(unchecked []wtsync.Unchecked) []string {
	var owed []string
	for _, u := range unchecked {
		owed = append(owed, u.Step)
	}
	return owed
}

// PrintUnchecked says which checks were not run and why.
func PrintUnchecked(w io.Writer, unchecked []wtsync.Unchecked) {
	for _, u := range unchecked {
		fmt.Fprintf(w, "  ✗ %s  owed: not checked: %s\n", u.Step, u.Why)
	}
}
