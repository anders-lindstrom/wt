package refpins

import (
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/git"
)

// DropEmptyRun deletes a run's meta once it has no pin left, so a run
// restored whole leaves nothing under refs/wt-swept/<runId>/.
func DropEmptyRun(root, runID string, w io.Writer) {
	runs, err := ReadSwept(root)
	if err != nil {
		return
	}
	for _, r := range runs {
		if r.RunID == runID && len(r.Refs) == 0 && r.MetaOID != "" {
			if err := DeleteRefAt(root, MetaRef(runID), r.MetaOID); err != nil {
				fmt.Fprintf(w, "! could not drop %s: %s\n", MetaRef(runID), git.Reason(err))
			}
		}
	}
}
