package purge

import (
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// Every field of a plan is in its token or left out on purpose, with why:
// a field added to the plan fails here until someone decides.
func TestPurgeTokenCoversEveryPlanField(t *testing.T) {
	t.Parallel()
	p := quarantine.PurgePlan{Dir: "/q", Pins: []quarantine.PurgePin{{Ref: "refs/x", OID: "o"}},
		Lost: []quarantine.PurgeLost{{Kind: "tip", OID: "o", Count: 1}}}
	tok := purgeTokenOf(p)
	gaps := jsonout.TokenGaps(tok, quarantine.PurgePlan{}, "", map[string]string{
		"Record":   "Data is the record byte for byte",
		"Bytes":    "Tree has every file's size",
		"Problems": "a plan with a problem refuses, and has no token",
	})
	gaps = append(gaps, jsonout.TokenGaps(tok, quarantine.Identity{}, "Identity.", nil)...)
	gaps = append(gaps, jsonout.TokenGaps(tok, quarantine.PurgePin{}, "Pins.", map[string]string{
		"Pins.Name": "Ref is where the pin is, which names it",
	})...)
	gaps = append(gaps, jsonout.TokenGaps(tok, quarantine.PurgeLost{}, "Lost.", nil)...)
	if len(gaps) > 0 {
		t.Errorf("decide each, in purgeTokenOf or here:\n%s", strings.Join(gaps, "\n"))
	}
}
