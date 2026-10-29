package repo

import "fmt"

// TrunkBase is a ref a branch can be merged into, and the commit it was at
// when it was read.
type TrunkBase struct {
	Name string // "origin/main" or "main"
	Tip  string
}

// TrunkBases resolves origin/<trunk> as last fetched, then <trunk>, to
// commits: what wt sweep and wt remove call merged. origin comes first
// because merges happen on the remote and the main checkout's trunk is often
// behind; the local trunk still counts, because commits on it are on trunk
// too. Nothing is fetched here. Neither being there is a *NoTrunkBaseError;
// a git that cannot say whether one is there is its own error.
func (r *Repo) TrunkBases(trunk string) ([]TrunkBase, error) {
	var bases []TrunkBase
	for _, b := range []struct{ name, ref string }{
		{"origin/" + trunk, "refs/remotes/origin/" + trunk},
		{trunk, "refs/heads/" + trunk},
	} {
		tip, ok, err := r.ResolveRef(b.ref)
		if err != nil {
			return nil, err
		}
		if ok {
			bases = append(bases, TrunkBase{Name: b.name, Tip: tip})
		}
	}
	if len(bases) == 0 {
		return nil, &NoTrunkBaseError{Trunk: trunk}
	}
	return bases, nil
}

// NoTrunkBaseError is a repository with neither origin/<trunk> nor <trunk>.
type NoTrunkBaseError struct{ Trunk string }

func (e *NoTrunkBaseError) Error() string {
	return fmt.Sprintf("neither origin/%s nor %s exists here, so nothing can be called merged", e.Trunk, e.Trunk)
}

// NoTrunkHere says there is nothing to compare a branch with.
func NoTrunkHere(trunk string) string {
	return "neither origin/" + trunk + " nor " + trunk + " is here"
}
