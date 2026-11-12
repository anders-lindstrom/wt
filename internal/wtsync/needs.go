package wtsync

import (
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// NeedDocker is the need of a step that cannot run without a Docker daemon.
const NeedDocker = "docker"

// KnownNeeds is every need a step may declare, each with the probe that
// tells whether it is there: a fixed set, so a declaration cannot name a
// command for wt to run as a probe.
var KnownNeeds = map[string]func() error{
	NeedDocker: probeDocker,
}

// probeDocker runs `docker info` under dockerDeadline.
func probeDocker() error {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	timedOut, _, err := git.RunBounded(dockerDeadline, cmd)
	if timedOut {
		return fmt.Errorf("docker info did not answer within %s", dockerDeadline)
	}
	if err != nil {
		return fmt.Errorf("docker info: %w", err)
	}
	return nil
}

// knownNeedNames is KnownNeeds' names, sorted, for a message.
func knownNeedNames() string {
	names := make([]string, 0, len(KnownNeeds))
	for n := range KnownNeeds {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func checkNeeds(needs []string) error {
	for _, n := range needs {
		if _, ok := KnownNeeds[n]; !ok {
			return fmt.Errorf("unknown need %q; the known needs are %s", n, knownNeedNames())
		}
	}
	return nil
}

// ProbeNeedsWith probes each of needs once with probes, and returns each
// probe's answer: nil for one that answered.
func ProbeNeedsWith(probes map[string]func() error, needs []string) map[string]error {
	got := map[string]error{}
	for _, n := range needs {
		if _, done := got[n]; done {
			continue
		}
		probe, ok := probes[n]
		if !ok {
			got[n] = fmt.Errorf("no probe for %q", n)
			continue
		}
		got[n] = probe()
	}
	return got
}

// ProbeNeeds is ProbeNeedsWith over KnownNeeds.
func ProbeNeeds(needs []string) map[string]error {
	return ProbeNeedsWith(KnownNeeds, needs)
}

// hasNeed reports whether any of needs is in want.
func hasNeed(needs []string, want string) bool {
	for _, n := range needs {
		if n == want {
			return true
		}
	}
	return false
}

// DeclaresNeed reports whether any step of the declaration needs n.
func (c *Config) DeclaresNeed(n string) bool {
	if c == nil {
		return false
	}
	for _, d := range c.Defer {
		if hasNeed(d.Needs, n) {
			return true
		}
	}
	for _, e := range c.EachCommit {
		if hasNeed(e.Needs, n) {
			return true
		}
	}
	return false
}
