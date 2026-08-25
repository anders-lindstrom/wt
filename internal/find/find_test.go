package find

import "testing"

func cand(work, branch, repo, path string, local bool) Candidate {
	return Candidate{Work: work, Branch: branch, Repo: repo, Path: path, Local: local}
}

func TestScoreTiers(t *testing.T) {
	c := cand("webkey_infra", "feat_wt/webkey_infra", "infrastructure", "/x/infrastructure_wt/feat_wt/webkey_infra", true)
	for _, tc := range []struct {
		pattern string
		want    int
	}{
		{"webkey_infra", 1},
		{"feat_wt/webkey_infra", 2},
		{"webkey", 3},
		{"key_inf", 4},
		{"infrastructure_wt", 5},
		{"wki", 6},
	} {
		got, ok := Score(c, tc.pattern, "")
		if !ok || got.Tier != tc.want {
			t.Errorf("Score(%q) tier = %d (ok=%v), want %d", tc.pattern, got.Tier, ok, tc.want)
		}
	}
	if _, ok := Score(c, "zzzz", ""); ok {
		t.Error("a non-matching pattern must not score")
	}
}

// A two-character pattern must not fall through to subsequence matching, or
// almost everything matches almost everything.
func TestShortPatternsDoNotSubsequenceMatch(t *testing.T) {
	c := cand("webkey_infra", "feat_wt/webkey_infra", "r", "/p", true)
	if _, ok := Score(c, "wi", ""); ok {
		t.Error("two-character subsequence should not match")
	}
}

func TestScoreIsCaseInsensitive(t *testing.T) {
	c := cand("WebKey", "feat_wt/WebKey", "r", "/p", true)
	if got, ok := Score(c, "webkey", ""); !ok || got.Tier != 1 {
		t.Errorf("got tier %d ok %v, want exact", got.Tier, ok)
	}
}

func TestRepoPatternFiltersByRepo(t *testing.T) {
	a := cand("arch", "feat_wt/arch", "accessmanager", "/a", false)
	p := cand("arch", "feat_wt/arch", "personal-v", "/p", false)
	if _, ok := Score(a, "arch", "personal"); ok {
		t.Error("accessmanager should be filtered out by repo pattern")
	}
	if _, ok := Score(p, "arch", "personal"); !ok {
		t.Error("personal-v should survive the repo pattern")
	}
}

// The pattern is data, not a regex: dots and brackets are literal.
func TestPatternIsNotARegex(t *testing.T) {
	c := cand("a.b", "feat_wt/a.b", "r", "/p", true)
	if got, ok := Score(c, "a.b", ""); !ok || got.Tier != 1 {
		t.Errorf("literal dot: tier %d ok %v", got.Tier, ok)
	}
	if _, ok := Score(cand("axb", "feat_wt/axb", "r", "/p", true), "a.b", ""); ok {
		t.Error("'.' must not act as a regex wildcard")
	}
}

func TestBestPrefersLowerTierThenLocal(t *testing.T) {
	scored := []Scored{
		{cand("a", "", "r1", "/a", false), 1},
		{cand("b", "", "r2", "/b", true), 1},
		{cand("c", "", "r3", "/c", true), 4},
	}
	best := Best(scored)
	if len(best) != 1 || best[0].Path != "/b" {
		t.Errorf("want the local tier-1 candidate, got %+v", best)
	}
}

func TestBestReturnsEveryTiedCandidate(t *testing.T) {
	scored := []Scored{
		{cand("arch", "", "accessmanager", "/a", false), 1},
		{cand("arch", "", "personal-v", "/p", false), 1},
	}
	if best := Best(scored); len(best) != 2 {
		t.Errorf("want both tied candidates, got %d", len(best))
	}
}

func TestBestOnEmptyIsEmpty(t *testing.T) {
	if best := Best(nil); len(best) != 0 {
		t.Errorf("want empty, got %v", best)
	}
}
