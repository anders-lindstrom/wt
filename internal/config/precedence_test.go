package config

import "testing"

// The repository outranks the person only where its file said something;
// the person's choice beats the repository's default; neither leaves the
// default.
func TestPrecedenceRepoFileThenUserFileThenDefault(t *testing.T) {
	t.Parallel()
	mine, err := loadUser(writeUser(t, "branch_suffix = \"\"\ntype_names = [\"feat=feature\", \"elsewhere=x\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	none := DefaultUser()
	repoSaid := &Config{BranchSuffix: "_wt", BranchSuffixSet: true, TypeNames: map[string]string{"feat": "feat"}, TypeNamesSet: true, Types: []string{"feat"}}
	repoSilent := &Config{BranchSuffix: "_wt", Types: []string{"feat"}}
	for _, tc := range []struct {
		name         string
		c            *Config
		u            *User
		suffix, sOri string
		feat, tOri   string
	}{
		{"repo file", repoSaid, mine, "_wt", "repo file", "feat", "repo file"},
		{"user file", repoSilent, mine, "", "user file", "feature", "user file"},
		{"default", repoSilent, none, "_wt", "repo default", "", "repo default"},
	} {
		if s, o := tc.c.BranchSuffixFor(tc.u); s != tc.suffix || o != tc.sOri {
			t.Errorf("%s: suffix %q from %s, want %q from %s", tc.name, s, o, tc.suffix, tc.sOri)
		}
		names, o := tc.c.TypeNamesFor(tc.u)
		if names["feat"] != tc.feat || o != tc.tOri || names["elsewhere"] != "" {
			t.Errorf("%s: names %v from %s, want feat=%q from %s", tc.name, names, o, tc.feat, tc.tOri)
		}
	}
}
