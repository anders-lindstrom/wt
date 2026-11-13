package synccmdtest

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/wtsync"
)

// DeclareSteps rewrites trunk's declaration as RunFixture's owned line for
// v.txt followed by steps, a defer: or each_commit: block, and fetches it.
func DeclareSteps(t *testing.T, ctx *kit.Context, steps string) {
	t.Helper()
	main := ctx.Repo.MainRoot
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n" + steps
	if err := os.WriteFile(filepath.Join(main, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitOut(t, main, "add", "-A")
	testkit.GitOut(t, main, "commit", "-q", "-m", "declare steps")
	testkit.GitOut(t, main, "fetch", "-q", "origin")
}

// Probe is a need's probe that answers Err and counts how often it was
// asked.
type Probe struct {
	mu    sync.Mutex
	calls int
	Err   error
}

// DockerDown is a docker probe that fails.
func DockerDown() *Probe { return &Probe{Err: errors.New("cannot connect to the Docker daemon")} }

// Calls is how often the probe was asked.
func (p *Probe) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Probes is the probe set a run's options take, with p as docker's.
func (p *Probe) Probes() map[string]func() error {
	return map[string]func() error{wtsync.NeedDocker: func() error {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.calls++
		return p.Err
	}}
}
