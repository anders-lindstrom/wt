package kit_test

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestWatchSignalsSIGINTStatus(t *testing.T) {
	t.Parallel()
	watchSignalStatus(t, syscall.SIGINT)
}

func TestWatchSignalsSIGTERMStatus(t *testing.T) {
	t.Parallel()
	watchSignalStatus(t, syscall.SIGTERM)
}

func watchSignalStatus(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != 128+int(sig) || !strings.Contains(out, "interrupted") {
			t.Fatalf("signal %s: status %d, output %q", sig, status, out)
		}
		return
	}
	defer kit.WatchSignals(os.Stderr, nil)()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
	select {}
}

// tagWriter prefixes everything written through it, so the parent can tell
// which handler printed.
type tagWriter struct {
	tag string
	w   *os.File
}

func (t tagWriter) Write(p []byte) (int, error) {
	_, err := fmt.Fprintf(t.w, "%s%s", t.tag, p)
	return len(p), err
}

// Once the inner watch ends, the outer one is the one a signal finds again.
func TestWatchSignalsOuterActsAgainOnceTheInnerEnds(t *testing.T) {
	t.Parallel()
	if os.Getenv(testkit.InterruptChild) == "" {
		out, status := testkit.InterruptedChild(t)
		if status != kit.InterruptStatus || !strings.Contains(out, "OUTER\ninterrupted") || strings.Contains(out, "INNER") {
			t.Fatalf("status %d; want only the outer handler:\n%s", status, out)
		}
		return
	}
	defer kit.WatchSignals(tagWriter{tag: "OUTER", w: os.Stderr}, nil)()
	kit.WatchSignals(tagWriter{tag: "INNER", w: os.Stderr}, nil)()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {}
}
