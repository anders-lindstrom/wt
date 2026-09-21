package config

import (
	"strings"
	"testing"
)

func raw(t *testing.T, s string) map[string]Value {
	t.Helper()
	v, err := ParseBash(strings.NewReader(s))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	return v
}

func TestUnknownKeyIsAnError(t *testing.T) {
	_, err := FromRaw(raw(t, "DEVELOPER_CONFIG_FILE=(.env)\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "DEVELOPER_CONFIG_FILE") {
		t.Fatalf("want error naming the key, got %v", err)
	}
}

func TestRetiredKeysExplainThemselves(t *testing.T) {
	for key, want := range map[string]string{
		"REPO_NAME":         "derived",
		"AWS_SETUP_ENABLED": "provision.sh",
		"WORKTREE_LAYOUT":   "no longer configurable",
	} {
		_, err := FromRaw(raw(t, key+"=x\n"), "main")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want error mentioning %q, got %v", key, want, err)
		}
	}
}

func TestMainBranchFallsBackToDetected(t *testing.T) {
	c, err := FromRaw(raw(t, ""), "trunk")
	if err != nil {
		t.Fatalf("FromRaw: %v", err)
	}
	if c.MainBranch != "trunk" {
		t.Errorf("MainBranch = %q, want trunk", c.MainBranch)
	}
}

func TestBuildInitCommandRequiredWhenEnabled(t *testing.T) {
	_, err := FromRaw(raw(t, "BUILD_INIT_ENABLED=true\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "BUILD_INIT_COMMAND") {
		t.Fatalf("want required-when-enabled error, got %v", err)
	}
	if _, err := FromRaw(raw(t, "BUILD_INIT_ENABLED=false\n"), "main"); err != nil {
		t.Errorf("disabled build init should not require a command: %v", err)
	}
}

// The rival-fix resolution: a prefix that names no valid type is an error
// naming WORKTREE_DEFAULT_TYPE as the remedy, rather than a silent guess.
func TestPrefixNamingNoValidTypeIsAnError(t *testing.T) {
	_, err := FromRaw(raw(t, "WORKTREE_BRANCH_PREFIX=wip\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "WORKTREE_DEFAULT_TYPE") {
		t.Fatalf("want error naming the remedy, got %v", err)
	}
}

func TestAllSevenRealConfigsValidate(t *testing.T) {
	for _, path := range mustGlob(t, "testdata/*.conf") {
		t.Run(path, func(t *testing.T) {
			c, err := FromRaw(mustParseFile(t, path), "main")
			if err != nil {
				t.Fatalf("FromRaw: %v", err)
			}
			if c.DefaultType != "feat" {
				t.Errorf("DefaultType = %q, want feat", c.DefaultType)
			}
		})
	}
}

// A MAIN_BRANCH nobody set is only a guess from the checkout, and a command
// that deletes what trunk contains has to know the difference.
func TestFromRawRecordsWhetherMainBranchWasSet(t *testing.T) {
	set, _ := FromRaw(map[string]Value{"MAIN_BRANCH": {Scalar: "trunk"}}, "guessed")
	if !set.MainBranchSet || set.MainBranch != "trunk" {
		t.Errorf("set: %+v", set)
	}
	unset, _ := FromRaw(map[string]Value{}, "guessed")
	if unset.MainBranchSet || unset.MainBranch != "guessed" {
		t.Errorf("unset: %+v", unset)
	}
	empty, _ := FromRaw(map[string]Value{"MAIN_BRANCH": {Scalar: ""}}, "guessed")
	if empty.MainBranchSet {
		t.Errorf("an empty MAIN_BRANCH is not set: %+v", empty)
	}
}

// SUPERSET_REGISTER is a three-way: a machine without Superset makes auto
// inert, so the interesting states are "on" (say when it is not there) and
// "off" (never look).
func TestSupersetRegister(t *testing.T) {
	for name, tc := range map[string]struct {
		raw     string
		want    SupersetMode
		problem string
	}{
		"unset":     {"", SupersetAuto, ""},
		"auto":      {"auto", SupersetAuto, ""},
		"on":        {"on", SupersetOn, ""},
		"off":       {"off", SupersetOff, ""},
		"uppercase": {"OFF", SupersetOff, ""},
		"empty":     {"", SupersetAuto, ""},
		"nonsense":  {"yes", SupersetAuto, `SUPERSET_REGISTER="yes" is not one of: auto on off`},
	} {
		t.Run(name, func(t *testing.T) {
			raw := map[string]Value{KeyMainBranch: {Scalar: "main"}}
			if tc.raw != "" {
				raw[KeySupersetRegister] = Value{Scalar: tc.raw}
			}
			c, err := FromRaw(raw, "main")
			if c.SupersetRegister != tc.want {
				t.Errorf("SupersetRegister = %q, want %q", c.SupersetRegister, tc.want)
			}
			switch {
			case tc.problem == "" && err != nil:
				t.Errorf("err = %v, want none", err)
			case tc.problem != "" && (err == nil || !strings.Contains(err.Error(), tc.problem)):
				t.Errorf("err = %v, want it to mention %q", err, tc.problem)
			}
		})
	}
}

// A list where a word belongs is a mistake worth a sentence: silently reading
// SUPERSET_REGISTER=(on) as the default left the repository asking for
// something it never got.
func TestSupersetRegisterRefusesAList(t *testing.T) {
	for name, v := range map[string]Value{
		"a list of one":  {List: []string{"on"}, IsList: true},
		"a list of many": {List: []string{"auto", "on"}, IsList: true},
		"an empty list":  {List: nil, IsList: true},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := FromRaw(map[string]Value{
				KeyMainBranch:       {Scalar: "main"},
				KeySupersetRegister: v,
			}, "main")
			if err == nil {
				t.Fatal("a list was accepted")
			}
			if !strings.Contains(err.Error(), "is a list; it takes one of: auto on off") {
				t.Errorf("err = %v", err)
			}
			if c.SupersetRegister != SupersetAuto {
				t.Errorf("SupersetRegister = %q, want the default", c.SupersetRegister)
			}
		})
	}
}

// WORKTREE_BRANCH_SUFFIX="" is the one string key where an empty value is a
// value: it is how a repository asks for feat/login-crash branches. It names
// branches only, so the type suffix — and with it every worktree path — is
// untouched.
func TestAnEmptyBranchSuffixIsAValue(t *testing.T) {
	c, err := FromRaw(raw(t, "WORKTREE_BRANCH_SUFFIX=\"\"\n"), "main")
	if err != nil {
		t.Fatalf("FromRaw: %v", err)
	}
	if c.BranchSuffix != "" || !c.BranchSuffixSet {
		t.Errorf("BranchSuffix = %q, set = %v; want \"\" true", c.BranchSuffix, c.BranchSuffixSet)
	}
	if c.TypeSuffix != DefaultTypeSuffix || c.BranchPrefix != "feat_wt" || c.DefaultType != "feat" {
		t.Errorf("type suffix %q, prefix %q, default type %q; want _wt feat_wt feat",
			c.TypeSuffix, c.BranchPrefix, c.DefaultType)
	}

	// A bare key says the same thing.
	if c, err := FromRaw(raw(t, "WORKTREE_BRANCH_SUFFIX=\n"), "main"); err != nil || c.BranchSuffix != "" || !c.BranchSuffixSet {
		t.Errorf("bare assignment: BranchSuffix = %q, set = %v, err %v", c.BranchSuffix, c.BranchSuffixSet, err)
	}
}

// An absent key is not a choice: the branch follows the folders, and the
// person's own setting is free to decide instead.
func TestAnAbsentBranchSuffixFollowsTheTypeSuffix(t *testing.T) {
	for conf, want := range map[string]string{
		"":                           DefaultTypeSuffix,
		"WORKTREE_TYPE_SUFFIX=-wt\n": "-wt",
	} {
		c, err := FromRaw(raw(t, conf), "main")
		if err != nil {
			t.Fatalf("FromRaw(%q): %v", conf, err)
		}
		if c.BranchSuffix != want || c.BranchSuffixSet {
			t.Errorf("%q: BranchSuffix = %q, set = %v; want %q false", conf, c.BranchSuffix, c.BranchSuffixSet, want)
		}
	}
}

// The prefix is a type spelled with the repository's own suffix, so a repo
// that changes the suffix does not have to restate the prefix to validate.
func TestTheBranchPrefixFollowsTheTypeSuffix(t *testing.T) {
	c, err := FromRaw(raw(t, "WORKTREE_TYPE_SUFFIX=-wt\n"), "main")
	if err != nil {
		t.Fatalf("FromRaw: %v", err)
	}
	if c.BranchPrefix != "feat-wt" || c.DefaultType != "feat" {
		t.Errorf("prefix %q, default type %q; want feat-wt feat", c.BranchPrefix, c.DefaultType)
	}
}

func TestASuffixWithASlashOrASpaceIsAnError(t *testing.T) {
	for _, conf := range []string{
		"WORKTREE_TYPE_SUFFIX=_wt/\n",
		"WORKTREE_BRANCH_SUFFIX=\"_wt \"\n",
	} {
		_, err := FromRaw(raw(t, conf), "main")
		if err == nil || !strings.Contains(err.Error(), "SUFFIX") {
			t.Errorf("%q: want an error naming the key, got %v", conf, err)
		}
	}
}
