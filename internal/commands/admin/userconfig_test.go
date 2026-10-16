package admin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// Doctor says the switch is off and how to change it, and does not count the
// person's own choice as a problem.
func TestDoctorSaysSupersetIsOffAtUserLevel(t *testing.T) {
	// Not parallel: swaps provision.ProbeSuperset.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	ctx.Config.SupersetRegister = config.SupersetOn
	provisiontest.Stub(t, superset.Status{Exe: "/nope/superset", Running: true})

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
	// Not parallel: sets environment variables for the whole test binary.
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
