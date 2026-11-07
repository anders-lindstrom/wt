// Package timemachine is wt's side of macOS's tmutil: whether a folder is
// excluded from Time Machine backups, and excluding it.
//
// The exclusion is tmutil's sticky one, `tmutil addexclusion <path>` with no
// -p: an extended attribute on the folder itself, which needs no sudo,
// follows the folder when it moves, and covers everything created inside it
// later.
package timemachine

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// Deadline bounds one tmutil call. tmutil answers in milliseconds; one that
// has not answered in this long is not going to. A variable for the test that
// proves a hang is given up on.
var Deadline = 10 * time.Second

// Tmutil is one tmutil executable.
type Tmutil struct{ Exe string }

// Find is the tmutil on the PATH. macOS keeps it in /usr/bin, so a Mac
// without one is a PATH that leaves /usr/bin out.
func Find() (Tmutil, bool) {
	exe, err := exec.LookPath("tmutil")
	if err != nil {
		return Tmutil{}, false
	}
	return Tmutil{Exe: exe}, true
}

// Excluded reports whether path carries an exclusion, as `tmutil isexcluded`
// prints it: [Excluded] or [Included].
func (t Tmutil) Excluded(path string) (bool, error) {
	out, err := t.run("isexcluded", path)
	if err != nil {
		return false, err
	}
	switch {
	case strings.Contains(out, "[Excluded]"):
		return true, nil
	case strings.Contains(out, "[Included]"):
		return false, nil
	}
	return false, fmt.Errorf("tmutil isexcluded %s: unexpected answer %q", path, firstLine(out))
}

// Exclude sets the sticky exclusion on path.
func (t Tmutil) Exclude(path string) error {
	_, err := t.run("addexclusion", path)
	return err
}

// run calls tmutil and returns its stdout. A failure carries tmutil's own
// line, which is the only thing that says what went wrong.
func (t Tmutil) run(args ...string) (string, error) {
	cmd := exec.Command(t.Exe, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	call := "tmutil " + strings.Join(args, " ")
	timedOut, _, err := git.RunBounded(Deadline, cmd)
	switch {
	case timedOut:
		return "", fmt.Errorf("%s did not answer within %s", call, Deadline)
	case err != nil:
		said := firstLine(stderr.String())
		if said == "" {
			said = firstLine(stdout.String())
		}
		if said == "" {
			return "", fmt.Errorf("%s: %w", call, err)
		}
		return "", fmt.Errorf("%s: %s", call, said)
	}
	return stdout.String(), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
