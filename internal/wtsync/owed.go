package wtsync

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

// Owed is what the last run of a branch left owed: the deferred steps that
// did not run or failed, for wt sync steps to run. It is kept under the
// common git dir, one file per branch, written by every run that finishes
// the branch's rebase and removed once nothing is owed.
type Owed struct {
	Branch string     `json:"branch"`
	Epoch  int64      `json:"epoch"`
	Steps  []OwedStep `json:"steps"`
}

// OwedStep is one deferred step owed: at a layer of a stack, or for the
// branch itself when Layer is empty.
type OwedStep struct {
	Step  string `json:"step"`
	Layer string `json:"layer,omitempty"`
	Why   string `json:"why"`
}

// StepSet is the owed steps by their run line.
func (o *Owed) StepSet() map[string]bool {
	set := map[string]bool{}
	if o == nil {
		return set
	}
	for _, s := range o.Steps {
		set[s.Step] = true
	}
	return set
}

func owedPath(mainRoot, branch string) (string, error) {
	common, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "wt-sync-owed", url.PathEscape(branch)+".json"), nil
}

// WriteOwed records o for its branch, atomically; with no steps it removes
// the branch's record.
func WriteOwed(mainRoot string, o *Owed) error {
	path, err := owedPath(mainRoot, o.Branch)
	if err != nil {
		return err
	}
	if len(o.Steps) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}

// ReadOwed reads what the last run of branch left owed; nil when nothing.
func ReadOwed(mainRoot, branch string) (*Owed, error) {
	path, err := owedPath(mainRoot, branch)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o Owed
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	return &o, nil
}
