package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// locatable builds a repo with one worktree per spec and returns the context.
func locatable(t *testing.T, specs ...string) *Context {
	t.Helper()
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for _, spec := range specs {
		if _, err := New(ctx, spec, NewOptions{NoSetup: true}, &buf); err != nil {
			t.Fatalf("New(%q): %v", spec, err)
		}
	}
	return ctx
}

// The WORK column of `wt list` is a valid argument whatever the type is. The
// old default-type fallback resolved a bare name to feat_wt/ and missed.
func TestLocateAcceptsWorkNameOfAnyType(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")

	got, err := Locate(ctx, "wt-migration")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got.Branch != "chore_wt/wt-migration" {
		t.Errorf("branch = %q, want chore_wt/wt-migration", got.Branch)
	}
}

// The BRANCH column is a valid argument, suffix and all.
func TestLocateAcceptsFullBranchName(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")

	got, err := Locate(ctx, "chore_wt/wt-migration")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got.Branch != "chore_wt/wt-migration" {
		t.Errorf("branch = %q, want chore_wt/wt-migration", got.Branch)
	}
}

// The form that already worked keeps working.
func TestLocateAcceptsTypeSlashWork(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")

	got, err := Locate(ctx, "chore/wt-migration")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got.Branch != "chore_wt/wt-migration" {
		t.Errorf("branch = %q, want chore_wt/wt-migration", got.Branch)
	}
}

// The PATH column is a valid argument.
func TestLocateAcceptsWorktreePath(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")
	want, err := Locate(ctx, "wt-migration")
	if err != nil {
		t.Fatal(err)
	}

	got, err := Locate(ctx, want.Path)
	if err != nil {
		t.Fatalf("Locate by path: %v", err)
	}
	if got.Path != want.Path {
		t.Errorf("path = %q, want %q", got.Path, want.Path)
	}
}

// A relative path is what tab completion and shell globbing hand over.
func TestLocateAcceptsRelativeWorktreePath(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")
	abs, err := Locate(ctx, "wt-migration")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(ctx.Repo.MainRoot, abs.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(ctx.Repo.MainRoot)

	got, err := Locate(ctx, rel)
	if err != nil {
		t.Fatalf("Locate by relative path: %v", err)
	}
	if got.Path != abs.Path {
		t.Errorf("path = %q, want %q", got.Path, abs.Path)
	}
}

// The main checkout is never a removal target, however it is named.
func TestLocateRefusesMainCheckout(t *testing.T) {
	ctx := locatable(t)

	if _, err := Locate(ctx, ctx.Repo.MainRoot); err == nil {
		t.Fatal("want an error locating the main checkout")
	} else if !strings.Contains(err.Error(), "main checkout") {
		t.Errorf("error should name the main checkout, got: %v", err)
	}
}

// One work name under two types is the only ambiguity exact matching allows,
// and it must never be resolved by guessing.
func TestLocateReportsAmbiguousWorkName(t *testing.T) {
	ctx := locatable(t, "feat/arch", "fix/arch")

	_, err := Locate(ctx, "arch")
	if err == nil {
		t.Fatal("want an error for a work name under two types")
	}
	for _, want := range []string{"feat_wt/arch", "fix_wt/arch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should list %s, got: %v", want, err)
		}
	}
}

// An exact type disambiguates what the bare work name cannot.
func TestLocateResolvesAmbiguousNameByType(t *testing.T) {
	ctx := locatable(t, "feat/arch", "fix/arch")

	got, err := Locate(ctx, "fix/arch")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got.Branch != "fix_wt/arch" {
		t.Errorf("branch = %q, want fix_wt/arch", got.Branch)
	}
}

// No fuzzy matching: `wt cd` may guess, `wt remove` may not.
func TestLocateDoesNotMatchPartialNames(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")

	if _, err := Locate(ctx, "wt-mig"); err == nil {
		t.Error("want an error for a partial name; remove must not guess")
	}
}

func TestLocateUnknownNameIsAnError(t *testing.T) {
	ctx := locatable(t, "chore/wt-migration")

	_, err := Locate(ctx, "nope")
	if err == nil {
		t.Fatal("want an error for a name that matches nothing")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should quote the argument, got: %v", err)
	}
}
