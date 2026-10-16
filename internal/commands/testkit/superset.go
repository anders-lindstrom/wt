package testkit

import (
	"os"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// OptIn is `wt config set superset true`. Every test that expects wt to reach
// Superset needs it: the shipped default is off.
func OptIn(ctx *kit.Context) {
	ctx.User.Superset = true
}

// ArgvOf is every argv line the fake wrote to log.
func ArgvOf(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// ErrBoom is an error for a fake to fail with.
var ErrBoom = boom{}

type boom struct{}

func (boom) Error() string { return "boom" }
