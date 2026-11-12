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
