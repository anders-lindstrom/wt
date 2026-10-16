package kit

import (
	"errors"
	"fmt"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Answers are the three keys a repository actually varies. Everything else is
// written commented at its default, where the file itself explains it.
type Answers struct {
	MainBranch   string
	BranchPrefix string
	BuildCommand string
}

// DetectAnswers is what `wt init` offers, and writes when nobody answers:
// trunk as detected, the default prefix, and no build command. A repository
// with no configuration runs on the same answers, so the two cannot differ.
func DetectAnswers(trunk string) Answers {
	return Answers{MainBranch: trunk, BranchPrefix: "feat_wt"}
}

// detectedConfig is the configuration a repository with no file runs on:
// DetectAnswers resolved, in memory, nothing written. No build command is
// detected, so nothing runs that the repository did not put there itself.
//
// An origin/HEAD naming a branch never fetched names nothing to branch from,
// and is refused as an undetectedError: `wt init` is where a person names it.
func detectedConfig(r *repo.Repo, trunk string) (*config.Config, error) {
	if !r.BranchExists(trunk) {
		if _, ok := r.ResolveRef("refs/remotes/origin/" + trunk); !ok {
			return nil, undetectedError{fmt.Errorf("cannot tell which branch is trunk: "+
				"origin/HEAD names %s, which is not fetched — run `wt init` (or set MAIN_BRANCH)", trunk)}
		}
	}
	c, err := ResolveAnswers(DetectAnswers(trunk))
	if err != nil {
		return nil, err
	}
	// The trunk is still only detected: nothing a person wrote says so.
	c.MainBranchSet, c.Detected = false, true
	return c, nil
}

// undetectedError is a repository whose trunk neither a MAIN_BRANCH nor its
// refs name. It is ErrNoConfig, which --json reports as noConfiguration.
type undetectedError struct{ err error }

func (e undetectedError) Error() string { return e.err.Error() }

func (e undetectedError) Unwrap() []error { return []error{e.err, config.ErrNoConfig} }

// ResolveAnswers runs the answers through the same validation the loader applies, so
// a rejected value is reported at the prompt rather than written and then
// discovered by the next command. The resolved Config is what render writes:
// taking the commented defaults from it, rather than from a second copy of the
// same table, is what keeps them honest.
func ResolveAnswers(a Answers) (*config.Config, error) {
	if a.MainBranch == "" {
		return nil, errors.New("trunk (MAIN_BRANCH) has no default: " +
			"no origin/HEAD and no development, main or master — name it")
	}
	raw := map[string]config.Value{
		config.KeyMainBranch:   {Scalar: a.MainBranch},
		config.KeyBranchPrefix: {Scalar: a.BranchPrefix},
	}
	if a.BuildCommand != "" {
		raw[config.KeyBuildInitCommand] = config.Value{Scalar: a.BuildCommand}
	}
	return config.FromRaw(raw, a.MainBranch)
}
