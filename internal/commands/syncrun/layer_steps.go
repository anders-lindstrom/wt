package syncrun

import (
	"fmt"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// LayerStepJSON is what one per-layer step came to at one layer, for --json.
type LayerStepJSON struct {
	Layer  int    `json:"layer"`
	Layers int    `json:"layers"`
	Branch string `json:"branch"`
	Step   string `json:"step"`
	Ran    bool   `json:"ran"`
	// Result is wtsync's: unchanged, folded, committed, skipped, owed,
	// failed.
	Result string  `json:"result"`
	Reason *string `json:"reason"`
	// Owner is the commit the output was folded into, as rewritten.
	Owner        *string `json:"owner"`
	OwnerSubject *string `json:"ownerSubject"`
	Commit       *string `json:"commit"`
	Proof        *string `json:"proof"`
}

// LayerStepsOf is steps for --json; never nil.
func LayerStepsOf(steps []wtsync.LayerStep) []LayerStepJSON {
	out := []LayerStepJSON{}
	for _, s := range steps {
		j := LayerStepJSON{Layer: s.Layer, Layers: s.Layers, Branch: s.Branch, Step: s.Step.Run, Ran: s.Ran, Result: s.Result,
			Reason: jsonout.Strp(kit.OneLine(s.Why))}
		if s.Owner != "" {
			j.Owner, j.OwnerSubject = jsonout.Strp(s.Owner), jsonout.Strp(s.OwnerSubject)
		}
		if s.Commit != "" {
			j.Commit = jsonout.Strp(s.Commit)
		}
		if s.Proof != "" {
			j.Proof = jsonout.Strp(s.Proof)
		}
		out = append(out, j)
	}
	return out
}

// OwedLayerSteps is the per-layer steps owed, named with their layer the
// way the closing error names owed steps.
func OwedLayerSteps(steps []wtsync.LayerStep) []string {
	var owed []string
	for _, s := range steps {
		if s.Owed() {
			owed = append(owed, fmt.Sprintf("%s at %s (layer %d/%d)", s.Step.Run, s.Branch, s.Layer, s.Layers))
		}
	}
	return owed
}

// FoldPaths is the paths of the declaration's per-layer steps.
func FoldPaths(cfg *wtsync.Config) []string {
	var paths []string
	for _, s := range cfg.LayerSteps() {
		paths = append(paths, s.Paths...)
	}
	return paths
}
