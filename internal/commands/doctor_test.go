package commands

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// A repository with no configuration is the one case where doctor's closing
// advice had nothing behind it: there are no migrate commands above to need
// fixing first, and no way out was named.
func TestDoctorTellsAConfiglessRepoToRunInit(t *testing.T) {
	r := bareRepo(t)
	ctx := OpenLenient(r.Root, io.Discard)
	if ctx == nil {
		t.Fatal("OpenLenient returned nil for a real repository")
	}

	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if problems == 0 {
		t.Fatal("Doctor found no problems in a repository with no configuration")
	}
	out := buf.String()
	if !strings.Contains(out, "wt init") {
		t.Errorf("doctor does not name the command that fixes it:\n%s", out)
	}
	if strings.Contains(out, "the migrate commands above") {
		t.Errorf("doctor advises fixing config for migrate commands it never printed:\n%s", out)
	}
}

// A configuration that exists but does not parse keeps the original advice:
// there the mutating commands really are what the fix unblocks.
func TestDoctorStillSaysToFixAConfigurationThatIsPresentButBroken(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nNO_SUCH_KEY=1\n")
	ctx := OpenLenient(main, io.Discard)

	var buf bytes.Buffer
	if _, err := Doctor(ctx, &buf); err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Fix the configuration first") {
		t.Errorf("doctor dropped the advice for a broken configuration:\n%s", out)
	}
	if strings.Contains(out, "wt init") {
		t.Errorf("doctor tells a configured repository to run wt init:\n%s", out)
	}
}
