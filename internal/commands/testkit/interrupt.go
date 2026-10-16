package testkit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// LockedBuffer is a bytes.Buffer a run goroutine may still be writing to
// while the test reads it.
type LockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *LockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *LockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// InterruptChild is set in the environment of a test binary that
// interruptedChild re-ran: the test runs its child half there.
const InterruptChild = "WT_TEST_INTERRUPT_CHILD"

// InterruptedChild re-runs the calling test in a child process, where its
// child half runs a command that is interrupted, and returns what the child
// printed and its exit status. An interrupt ends the child with os.Exit and
// no cleanups, so its temporary files go under this test's TempDir instead.
func InterruptedChild(t *testing.T) (out string, status int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1") //nolint:gosec // G702: re-runs this test binary with its own test name
	cmd.Env = append(os.Environ(), InterruptChild+"=1", "TMPDIR="+t.TempDir())
	b, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return string(b), cmd.ProcessState.ExitCode()
}

// InterruptWhileHandingOver makes the next sidecar write in gitDir block for
// good, on a FIFO nobody opens for reading, and sends this process SIGINT once
// briefWritten says the brief is on disk. The brief is written just before the
// sidecar, so the signal lands in the middle of writing the handover.
func InterruptWhileHandingOver(t *testing.T, gitDir string, briefWritten func() bool) {
	t.Helper()
	if err := syscall.Mkfifo(wtsync.StatePath(gitDir)+".tmp", 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for !briefWritten() {
			time.Sleep(10 * time.Millisecond)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
}
