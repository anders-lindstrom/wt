package repo

import (
	"errors"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// origin's trunk comes before the local one, each at its own tip; with
// neither there the answer is a *NoTrunkBaseError.
func TestTrunkBasesOriginFirstThenLocal(t *testing.T) {
	t.Parallel()
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	r := mustDiscover(t, dir)
	bases, err := r.TrunkBases("main")
	if err != nil || len(bases) != 1 || bases[0].Name != "main" {
		t.Fatalf("no origin: %+v, %v", bases, err)
	}
	withOriginNoHead(t, dir)
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "local only")
	bases, err = r.TrunkBases("main")
	if err != nil || len(bases) != 2 || bases[0].Name != "origin/main" || bases[1].Name != "main" || bases[0].Tip == bases[1].Tip {
		t.Fatalf("with origin: %+v, %v", bases, err)
	}
	var none *NoTrunkBaseError
	if _, err := r.TrunkBases("trunk"); !errors.As(err, &none) || none.Trunk != "trunk" {
		t.Fatalf("no such trunk: %v", err)
	}
}
