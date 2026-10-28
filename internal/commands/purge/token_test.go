package purge

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// Every field of a plan is in its token or left out on purpose, with why:
// a field added to the plan fails here until someone decides.
func TestRefsPurgeTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	p := RefsPurgePlan{Repo: "/r", Runs: []purgeRun{{SweptRun: refpins.SweptRun{RunID: "run", MetaOID: "m",
		Refs: []refpins.SweptRef{{Pin: "refs/wt-swept/run/heads/a", Object: "o"}}}}}}
	tok := purgeRefsTokenOf(p)
	gaps := kit.TokenGaps(tok, RefsPurgePlan{}, "", map[string]string{
		"OlderThan": "it picks the runs, and the runs it picked are in it",
	})
	gaps = append(gaps, kit.TokenGaps(tok, purgeRun{}, "Runs.", map[string]string{
		"Runs.At":   "it picks the run under --older-than, and the run is in it",
		"Runs.Meta": "MetaOID is the meta's blob",
		"Runs.Lost": "what deleting the pins loses is reported, not acted on; the pins are in it",
	})...)
	gaps = append(gaps, kit.TokenGaps(tok, refpins.SweptRef{}, "Runs.Refs.", map[string]string{
		"Runs.Refs.RunID": "the pin names it",
		"Runs.Refs.Kind":  "the pin names it",
		"Runs.Refs.Name":  "the pin names it",
	})...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in purgeRefsTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}

func TestPurgeTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	p := quarantine.PurgePlan{Dir: "/q", Pins: []quarantine.PurgePin{{Ref: "refs/x", OID: "o"}},
		Lost: []quarantine.PurgeLost{{Kind: "tip", OID: "o", Count: 1}}}
	tok := purgeTokenOf(p)
	gaps := kit.TokenGaps(tok, quarantine.PurgePlan{}, "", map[string]string{
		"Record":   "Data is the record byte for byte",
		"Bytes":    "Tree has every file's size",
		"Problems": "a plan with a problem refuses, and has no token",
	})
	gaps = append(gaps, kit.TokenGaps(tok, quarantine.Identity{}, "Identity.", nil)...)
	gaps = append(gaps, kit.TokenGaps(tok, quarantine.PurgePin{}, "Pins.", map[string]string{
		"Pins.Name": "Ref is where the pin is, which names it",
	})...)
	gaps = append(gaps, kit.TokenGaps(tok, quarantine.PurgeLost{}, "Lost.", nil)...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in purgeTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}
