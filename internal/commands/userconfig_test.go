package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// userConfigIn points this test's user config at a directory of its own.
func userConfigIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "wt", "config.toml")
}

// With superset off, SUPERSET_REGISTER=on still gets nothing: no subprocess,
// nothing on stderr.
func TestUserSupersetOffBeatsTheRepository(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	ctx.Config.SupersetRegister = config.SupersetOn
	log := fakeSuperset(t, ctx.Repo.MainRoot, true)

	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if got := argvOf(t, log); got != nil {
		t.Errorf("superset was run despite the user switch: %q", got)
	}
	if line := supersetLine(errs.String()); line != "" {
		t.Errorf("stderr mentions Superset: %q", line)
	}
}

// Doctor says the switch is off and how to change it, and does not count the
// person's own choice as a problem.
func TestDoctorSaysSupersetIsOffAtUserLevel(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	ctx.Config.SupersetRegister = config.SupersetOn
	stub(t, superset.Status{Exe: "/nope/superset", Running: true})

	var out bytes.Buffer
	problems, err := Doctor(ctx, &out)
	if err != nil {
		t.Fatal(err)
	}
	if problems != 0 {
		t.Errorf("problems = %d, want 0:\n%s", problems, out.String())
	}
	want := "  - off in your wt config; turn it on with `wt config set superset true`"
	if !strings.Contains(out.String(), want) {
		t.Errorf("doctor said\n%s\nwant %q", out.String(), want)
	}
	// One remark and nothing else: probing a Superset the person has switched
	// off would be the very subprocess the switch exists to prevent.
	if strings.Contains(out.String(), "host service") {
		t.Errorf("doctor probed Superset anyway:\n%s", out.String())
	}
}

// A broken user file is a doctor finding, not a reason for doctor to refuse.
func TestDoctorReportsABrokenUserConfig(t *testing.T) {
	path := userConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("superst = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := committedRepo(t, minimalConf)
	ctx := OpenLenient(main, &bytes.Buffer{})

	var out bytes.Buffer
	problems, err := Doctor(ctx, &out)
	if err != nil {
		t.Fatal(err)
	}
	if problems == 0 || !strings.Contains(out.String(), `unknown key "superst"`) {
		t.Errorf("problems = %d, doctor said\n%s", problems, out.String())
	}
}

// The plain print says where each value came from, including which file
// decided Superset.
func TestConfigPrintsTheUserSettings(t *testing.T) {
	userConfigIn(t)
	ctx, err := Open(fixtureRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, false, &buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(no file yet)",
		"  superset:    false (default)",
		"  github:      true (default)",
		"superset mode: off (user config)",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("config said\n%s\nwant a line %q", buf.String(), want)
		}
	}
}

// Once opted in, the repository's key decides the mode and the print names
// the file.
func TestConfigNamesTheFileThatDecidedSuperset(t *testing.T) {
	userConfigIn(t)
	for name, tc := range map[string]struct{ conf, want string }{
		"repo left it alone": {minimalConf, "superset mode: auto (repo default)"},
		"repo set it":        {minimalConf + "SUPERSET_REGISTER=on\n", "superset mode: on (repo file)"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := Open(fixtureRepo(t, tc.conf))
			if err != nil {
				t.Fatal(err)
			}
			optIn(ctx)
			var buf bytes.Buffer
			if err := Config(ctx, false, &buf); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("config said\n%s\nwant %q", buf.String(), tc.want)
			}
		})
	}
}

// --shell is a contract the Herdr skills eval: the repository's assignments
// and nothing else.
func TestConfigShellSaysNothingAboutTheUserSettings(t *testing.T) {
	userConfigIn(t)
	ctx, err := Open(fixtureRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		key, _, _ := strings.Cut(line, "=")
		if key != strings.ToUpper(key) {
			t.Errorf("--shell emitted a non-legacy assignment: %q", line)
		}
	}
	if strings.Contains(buf.String(), "user config") {
		t.Errorf("--shell mentions the user config:\n%s", buf.String())
	}
}

// set, get and unset round-trip, none of them needing a repository.
func TestUserSetGetUnsetFromOutsideARepository(t *testing.T) {
	path := userConfigIn(t)
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	if err := UserSet(config.UserKeySuperset, "true", &out); err != nil {
		t.Fatalf("UserSet: %v", err)
	}
	if !strings.Contains(out.String(), "superset = true in "+path) {
		t.Errorf("set said %q", out.String())
	}

	out.Reset()
	if err := UserGet(config.UserKeySuperset, &out, io.Discard); err != nil {
		t.Fatalf("UserGet: %v", err)
	}
	if strings.TrimSpace(out.String()) != "true" {
		t.Errorf("get said %q, want the value alone", out.String())
	}

	out.Reset()
	if err := UserUnset(config.UserKeySuperset, &out); err != nil {
		t.Fatalf("UserUnset: %v", err)
	}
	if !strings.Contains(out.String(), "back to false") {
		t.Errorf("unset said %q", out.String())
	}

	out.Reset()
	if err := UserGet(config.UserKeySuperset, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "false" {
		t.Errorf("after unset, get said %q", out.String())
	}
}

func TestUserSetRejectsWhatItCannotWrite(t *testing.T) {
	path := userConfigIn(t)
	for name, tc := range map[string]struct{ key, value, want string }{
		"unknown key":     {"superst", "true", `unknown key "superst"`},
		"not a boolean":   {config.UserKeySuperset, "sometimes", "is not a boolean"},
		"repository key":  {"SUPERSET_REGISTER", "off", "unknown key"},
		"empty key given": {"", "true", "unknown key"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := UserSet(tc.key, tc.value, &out)
			if err == nil {
				t.Fatalf("accepted; it said %q", out.String())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a rejected set wrote the file anyway")
			}
		})
	}
}

// A user file wt cannot read stops the commands that must have it right.
func TestOpenRefusesABrokenUserConfig(t *testing.T) {
	path := userConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("github = \"yes\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(fixtureRepo(t, minimalConf))
	if err == nil {
		t.Fatal("a broken user config was accepted")
	}
	if !strings.Contains(err.Error(), "not a boolean") || !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name the file and the problem", err)
	}
}
