package provision_test

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
)

// With superset off, SUPERSET_REGISTER=on still gets nothing: no subprocess,
// nothing on stderr.
func TestUserSupersetOffBeatsTheRepository(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	ctx.Config.SupersetRegister = config.SupersetOn
	log := provisiontest.FakeSuperset(t, ctx, ctx.Repo.MainRoot, true)

	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("superset was run despite the user switch: %q", got)
	}
	if line := supersetLine(errs.String()); line != "" {
		t.Errorf("stderr mentions Superset: %q", line)
	}
}
