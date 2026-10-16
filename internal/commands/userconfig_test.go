package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// A file that parses with one bad key keeps its good keys and its defaults,
// and the warning names the part being ignored rather than claiming the whole
// file was thrown away. `wt doctor` still counts it as a problem.
func TestOpenWarnsAboutABrokenUserConfigAndCarriesOn(t *testing.T) {
	// Not parallel: sets environment variables for the whole test binary.
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
	// Not parallel: sets environment variables for the whole test binary.
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
	log := testkit.FakeGitHub(t, ctx, testkit.OpenPR(12, "fix_wt/login-crash", "Login crash"))
	var out bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
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
