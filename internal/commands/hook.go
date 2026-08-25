package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// hookInput is the JSON Claude Code sends on stdin. Unknown fields are ignored
// so a new field in the harness does not break the hook.
type hookInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
	CWD  string `json:"cwd"`
}

// HookCreate handles Claude Code's WorktreeCreate event. The contract is that
// the absolute worktree path is the only thing on stdout; everything else goes
// to stderr, or the harness cannot read the result.
func HookCreate(ctx *Context, in io.Reader, out, logw io.Writer) error {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return fmt.Errorf("reading hook input: %w", err)
	}
	if input.Name == "" {
		return errors.New("'name' is required in the hook input")
	}
	path, err := New(ctx, input.Name, NewOptions{}, logw)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

// HookRemove handles Claude Code's WorktreeRemove event, going through the same
// merge-checked removal as `wt remove` so the harness cannot lose unmerged work.
//
// Unlike HookCreate there is no result for the harness to read, so this writes
// nothing to stdout and takes no writer for it.
func HookRemove(ctx *Context, in io.Reader, logw io.Writer) error {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil {
		return fmt.Errorf("reading hook input: %w", err)
	}
	target := input.Path
	if target == "" {
		if input.Name == "" {
			return errors.New("'path' or 'name' is required in the hook input")
		}
		p, err := Path(ctx, input.Name)
		if err != nil {
			return err
		}
		target = p
	}
	return RemoveAt(ctx, target, logw)
}
