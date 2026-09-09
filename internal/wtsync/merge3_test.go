package wtsync

import (
	"errors"
	"strings"
	"testing"
)

func conflict(base, trunk, branch string) Conflict {
	return Conflict{Path: "f.txt", Base: []byte(base), Trunk: []byte(trunk), Branch: []byte(branch)}
}

func TestMerge3SplitsTextAndBlocks(t *testing.T) {
	segs, err := Merge3(conflict("keep\nbase\ntail\n", "keep\ntrunk\ntail\n", "keep\nbranch\ntail\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 {
		t.Fatalf("segments = %d, want 3: %+v", len(segs), segs)
	}
	if segs[0].Block != nil || strings.Join(segs[0].Lines, "|") != "keep" {
		t.Errorf("first segment = %+v", segs[0])
	}
	b := segs[1].Block
	if b == nil || strings.Join(b.Branch, "|") != "branch" || strings.Join(b.Base, "|") != "base" || strings.Join(b.Trunk, "|") != "trunk" {
		t.Errorf("block = %+v", b)
	}
	if segs[2].Block != nil || strings.Join(segs[2].Lines, "|") != "tail" {
		t.Errorf("last segment = %+v", segs[2])
	}
}

func TestMerge3KeepsOneSidedChangesOutsideBlocks(t *testing.T) {
	segs, err := Merge3(conflict("a\nv1\n", "a\nv2\ntrunk-added\n", "a\nv3\n"))
	if err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, s := range segs {
		if s.Block == nil {
			text = append(text, s.Lines...)
		}
	}
	joined := strings.Join(text, "|")
	if !strings.Contains(joined, "a") {
		t.Errorf("text segments = %q", joined)
	}
}

func TestMerge3ReportsNoBlocksWhenGitMergesCleanly(t *testing.T) {
	// The two edits need unchanged context lines between them: git folds
	// adjacent edits into one conflict block when there's no context to
	// keep them apart.
	segs, err := Merge3(conflict("x\na\ny\nb\nz\n", "x\na2\ny\nb\nz\n", "x\na\ny\nb2\nz\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range segs {
		if s.Block != nil {
			t.Fatalf("unexpected block %+v", s.Block)
		}
	}
}

func TestRenderReplacesBlocksAndPreservesTheEnding(t *testing.T) {
	c := conflict("x\nbase\n", "x\ntrunk\n", "x\nbranch\n")
	segs, err := Merge3(c)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(segs, func(_ Block) ([]string, error) { return []string{"resolved"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "x\nresolved\n" {
		t.Errorf("out = %q", out)
	}
	// no trailing newline on the branch side -> none in the output
	c2 := conflict("x\nbase", "x\ntrunk", "x\nbranch")
	segs2, _ := Merge3(c2)
	out2, _ := Render(segs2, func(_ Block) ([]string, error) { return []string{"r"}, nil })
	if string(out2) != "x\nr" {
		t.Errorf("out2 = %q", out2)
	}
}

func TestRenderPropagatesARefusal(t *testing.T) {
	segs, _ := Merge3(conflict("base\n", "trunk\n", "branch\n"))
	_, err := Render(segs, func(b Block) ([]string, error) { return nil, Refuse("f.txt", "not mine: %d lines", len(b.Branch)) })
	var r *Refusal
	if !errors.As(err, &r) || r.Path != "f.txt" || !strings.Contains(r.Reason, "not mine: 1 lines") {
		t.Errorf("err = %v", err)
	}
	if !IsRefusal(err) {
		t.Error("IsRefusal should be true")
	}
}
