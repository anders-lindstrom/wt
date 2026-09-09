package wtsync

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Block is one region git could not merge: the branch's lines, the base's,
// and trunk's.
type Block struct {
	Branch []string
	Base   []string
	Trunk  []string
}

// Segment is a run of merged text, or one conflict block. A zero-value
// Segment (both fields nil) carries no content: Merge3 appends one as a
// trailing marker meaning "the branch blob had no final newline."
type Segment struct {
	Lines []string
	Block *Block
}

const (
	markBranch = "<<<<<<< branch"
	markBase   = "||||||| base"
	markMid    = "======="
	markTrunk  = ">>>>>>> trunk"
)

// Merge3 merges the three blobs with git's own three-way merge and splits the
// result into text and conflict blocks. git decides what conflicts; this
// package only decides what to do about it.
func Merge3(c Conflict) ([]Segment, error) {
	dir, err := os.MkdirTemp("", "wtsync-merge3-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	names := map[string][]byte{"branch": c.Branch, "base": c.Base, "trunk": c.Trunk}
	for name, data := range names {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command("git", "merge-file", "-p", "--diff3",
		"-L", "branch", "-L", "base", "-L", "trunk",
		filepath.Join(dir, "branch"), filepath.Join(dir, "base"), filepath.Join(dir, "trunk"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// merge-file exits with the number of conflicts, or negative on error.
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() < 0 || exit.ExitCode() > 127 {
			return nil, errors.New(strings.TrimSpace(stderr.String()))
		}
	}
	segs, err := split(stdout.String())
	if err != nil {
		return nil, err
	}
	// git always newline-terminates its ">>>>>>> trunk" marker, so the raw
	// merge-file output can't say whether the branch blob itself ended in a
	// newline when a block sits at EOF. Ask the branch blob directly.
	// Render assumes a trailing newline by default; this zero-value sentinel
	// segment is only needed to say there wasn't one.
	if !bytes.HasSuffix(c.Branch, []byte("\n")) {
		segs = append(segs, Segment{})
	}
	return segs, nil
}

// split walks merge-file's output. A line that is not inside a block is
// text; the four markers delimit the branch, base and trunk sides.
func split(out string) ([]Segment, error) {
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if out == "" {
		lines = nil
	}
	var segs []Segment
	var text []string
	var blk *Block
	side := ""
	flushText := func() {
		if len(text) > 0 {
			segs = append(segs, Segment{Lines: text})
			text = nil
		}
	}
	for _, l := range lines {
		switch {
		case blk == nil && l == markBranch:
			flushText()
			blk, side = &Block{}, "branch"
		case blk != nil && l == markBase:
			side = "base"
		case blk != nil && l == markMid:
			side = "trunk"
		case blk != nil && l == markTrunk:
			segs = append(segs, Segment{Block: blk})
			blk, side = nil, ""
		case blk != nil:
			switch side {
			case "branch":
				blk.Branch = append(blk.Branch, l)
			case "base":
				blk.Base = append(blk.Base, l)
			case "trunk":
				blk.Trunk = append(blk.Trunk, l)
			}
		default:
			text = append(text, l)
		}
	}
	if blk != nil {
		return nil, errors.New("unterminated conflict block in merge output")
	}
	flushText()
	return segs, nil
}

// Render emits the merged file with each block replaced by collapse's lines.
// The output ends with a newline exactly when the branch blob did: Merge3
// marks the absence of one with a zero-value trailing segment, since a
// newline ending is the default.
func Render(segs []Segment, collapse func(Block) ([]string, error)) ([]byte, error) {
	trailingNewline := true
	var out []string
	for _, s := range segs {
		if s.Block == nil && s.Lines == nil {
			trailingNewline = false
			continue
		}
		if s.Block == nil {
			out = append(out, s.Lines...)
			continue
		}
		lines, err := collapse(*s.Block)
		if err != nil {
			return nil, err
		}
		out = append(out, lines...)
	}
	result := strings.Join(out, "\n")
	if trailingNewline {
		result += "\n"
	}
	return []byte(result), nil
}
