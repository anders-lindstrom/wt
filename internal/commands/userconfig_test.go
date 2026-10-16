package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// With superset off, SUPERSET_REGISTER=on still gets nothing: no subprocess,
// nothing on stderr.
func TestUserSupersetOffBeatsTheRepository(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	ctx.Config.SupersetRegister = config.SupersetOn
	log := fakeSuperset(t, ctx.Repo.MainRoot, true)

	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("superset was run despite the user switch: %q", got)
	}
	if line := supersetLine(errs.String()); line != "" {
		t.Errorf("stderr mentions Superset: %q", line)
	}
}

// Doctor says the switch is off and how to change it, and does not count the
// person's own choice as a problem.
func TestDoctorSaysSupersetIsOffAtUserLevel(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
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
	path := testkit.UserConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("superst = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx := kit.OpenLenient(main, &bytes.Buffer{})

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
	testkit.UserConfigIn(t)
	ctx, err := kit.Open(testkit.FixtureRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, false, &buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"(no file yet)",
		"  superset:      false (default)",
		"  github:        true (default)",
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
	testkit.UserConfigIn(t)
	for name, tc := range map[string]struct{ conf, want string }{
		"repo left it alone": {testkit.MinimalConf, "superset mode: auto (repo default)"},
		"repo set it":        {testkit.MinimalConf + "SUPERSET_REGISTER=on\n", "superset mode: on (repo file)"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.FixtureRepo(t, tc.conf))
			if err != nil {
				t.Fatal(err)
			}
			testkit.OptIn(ctx)
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
	testkit.UserConfigIn(t)
	ctx, err := kit.Open(testkit.FixtureRepo(t, testkit.MinimalConf))
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
	path := testkit.UserConfigIn(t)
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
	path := testkit.UserConfigIn(t)
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

// A file that parses with one bad key keeps its good keys and its defaults,
// and the warning names the part being ignored rather than claiming the whole
// file was thrown away. `wt doctor` still counts it as a problem.
func TestOpenWarnsAboutABrokenUserConfigAndCarriesOn(t *testing.T) {
	path := testkit.UserConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("github = \"yes\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := kit.Open(testkit.FixtureRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatalf("a broken user config stopped the command: %v", err)
	}
	if !ctx.UserConfig().GitHub || ctx.UserConfig().Superset {
		t.Errorf("user settings = %+v, want the defaults", ctx.UserConfig())
	}

	var errs bytes.Buffer
	ctx.WarnTo(&errs)
	if !strings.Contains(errs.String(), "not a boolean") || !strings.Contains(errs.String(), path) {
		t.Errorf("stderr = %q, want it to name the file and the problem", errs.String())
	}
	if !strings.HasPrefix(errs.String(), "wt: ignoring ") {
		t.Errorf("stderr = %q, want it to say which part is being ignored", errs.String())
	}
	if strings.Contains(errs.String(), "default settings") {
		t.Errorf("stderr = %q, but the keys that parsed were kept", errs.String())
	}
	if n := strings.Count(errs.String(), "\n"); n != 1 {
		t.Errorf("stderr = %q, want exactly one line", errs.String())
	}
	// Whatever else the command then asks, the file is complained about once.
	ctx.Warnf(kit.WarnUserConfig, "again")
	if n := strings.Count(errs.String(), "\n"); n != 1 {
		t.Errorf("stderr = %q, want the warning only once", errs.String())
	}
}

// A file wt cannot parse at all turns every integration off for the run. The
// command still works; what stops is anything that would have run gh or
// Superset on the strength of a default the person never asked for.
func TestAUserFileThatDoesNotParseTurnsEveryIntegrationOff(t *testing.T) {
	path := testkit.WriteUserConfig(t, "github = false\nsuperset = \n")
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatalf("a user file that does not parse stopped the command: %v", err)
	}
	if ctx.UserConfig().GitHub || ctx.UserConfig().Superset {
		t.Errorf("user settings = %+v, want every integration off", ctx.UserConfig())
	}

	var errs bytes.Buffer
	ctx.WarnTo(&errs)
	if !strings.Contains(errs.String(), "every integration is off for this run") {
		t.Errorf("stderr = %q, want it to say the integrations are off", errs.String())
	}
	if !strings.Contains(errs.String(), path) {
		t.Errorf("stderr = %q, want it to name the file", errs.String())
	}
	if n := strings.Count(errs.String(), "\n"); n != 1 {
		t.Errorf("stderr = %q, want exactly one line", errs.String())
	}

	// The one thing that must not happen: a gh process.
	log := testkit.FakeGitHub(t, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var out bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if err := List(ctx, ListOptions{}, &out, 0); err != nil {
		t.Fatal(err)
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("gh was run for a user file wt could not read: %q", got)
	}
	if strings.Contains(out.String(), "#12") {
		t.Errorf("wt list showed a pull request anyway:\n%s", out.String())
	}
}

// `wt config get` answers with the value wt itself is acting on, and puts
// what is wrong with the file on stderr: a script asking what a setting is
// must not fail because of an unrelated key.
func TestUserGetAnswersDespiteABrokenFile(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"one bad key":         {"superset = true\ngithbu = false\n", "true"},
		"does not parse":      {"superset = true\ngithub = \n", "false"},
		"not a boolean value": {"superset = \"yes\"\n", "false"},
	} {
		t.Run(name, func(t *testing.T) {
			path := testkit.WriteUserConfig(t, tc.body)
			var out, errs bytes.Buffer
			if err := UserGet(config.UserKeySuperset, &out, &errs); err != nil {
				t.Fatalf("UserGet: %v", err)
			}
			if got := strings.TrimSpace(out.String()); got != tc.want {
				t.Errorf("stdout = %q, want %q", got, tc.want)
			}
			if !strings.Contains(errs.String(), path) {
				t.Errorf("stderr = %q, want the file's problem on it", errs.String())
			}
		})
	}
}

// An unknown key is still an error: there is no value to answer with.
func TestUserGetStillRefusesAnUnknownKey(t *testing.T) {
	testkit.WriteUserConfig(t, "superset = true\n")
	var out, errs bytes.Buffer
	if err := UserGet("superst", &out, &errs); err == nil {
		t.Fatalf("an unknown key was answered with %q", out.String())
	}
}

// `wt config` says on stdout that it could not read the file, so the false
// beside every setting is not read as somebody's choice.
func TestConfigSaysTheFileCouldNotBeRead(t *testing.T) {
	path := testkit.WriteUserConfig(t, "github = \n")
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Config(ctx, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), path+" (wt cannot read it") {
		t.Errorf("wt config said\n%s\nwant the file marked unreadable", out.String())
	}
	if !strings.Contains(out.String(), "github:        false (file unreadable)") {
		t.Errorf("wt config said\n%s\nwant each value marked as coming from nowhere", out.String())
	}
}
