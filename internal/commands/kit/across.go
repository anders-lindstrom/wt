package kit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/reposel"
)

// AcrossRepos runs fn in every repository a selection names, a few at a time,
// and prints what each printed under its name, in the selection's order. A
// repository fn cannot run in is reported under its name, the rest go on, and
// the returned error names every one that failed.
func AcrossRepos(u *config.User, sel reposel.Selection, w io.Writer, fn func(*Context, io.Writer) error) error {
	set, err := reposel.Select(u, sel)
	if err != nil {
		return err
	}
	type result struct {
		out bytes.Buffer
		err error
	}
	results := reposel.Each(set.Repos, reposel.Parallelism, func(t reposel.Target) *result {
		r := &result{}
		if t.Problem != "" {
			r.err = fmt.Errorf("%s: %s", t.Path, t.Problem)
			return r
		}
		ctx, err := Open(t.Path, io.Discard)
		if err != nil {
			r.err = err
			return r
		}
		r.err = fn(ctx, &r.out)
		return r
	})
	home, _ := os.UserHomeDir()
	var failed []string
	for i, r := range results {
		t := set.Repos[i]
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "== %s  %s\n", t.Name, AbbreviateHome(t.Path, home))
		_, _ = w.Write(r.out.Bytes())
		if r.err != nil {
			fmt.Fprintf(w, "! %v\n", r.err)
			failed = append(failed, t.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %s failed: %s", len(failed), Count(len(results), "repository", "repositories"), strings.Join(failed, ", "))
	}
	return nil
}
