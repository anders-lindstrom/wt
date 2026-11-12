package wtsync

import (
	"strings"
	"testing"
)

func TestParseReadsEachLayerAndFoldOwner(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`
defer:
  - run: ./gen.sh
    paths: [gen/**]
    each: layer
    fold: owner
    commit: "chore: regenerate"
  - run: ./other.sh
`))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Defer[0]
	if d.Each != EachLayer || d.Fold != FoldOwner || !d.PerLayer() {
		t.Errorf("defer[0] %+v", d)
	}
	if o := cfg.Defer[1]; o.Each != "" || o.Fold != "" || o.PerLayer() {
		t.Errorf("defer[1] %+v", o)
	}
	if got := cfg.LayerSteps(); len(got) != 1 || got[0].Run != "./gen.sh" {
		t.Errorf("layer steps %+v", got)
	}
}

func TestParseRefusesABadEachOrFold(t *testing.T) {
	t.Parallel()
	for yaml, want := range map[string]string{
		"defer:\n  - run: ./gen.sh\n    each: commit\n":                     `defer[0]: unknown each "commit"`,
		"defer:\n  - run: ./gen.sh\n    paths: [gen/**]\n    fold: first\n": `defer[0]: unknown fold "first"`,
		"defer:\n  - run: ./gen.sh\n    each: layer\n    fold: owner\n":     "defer[0]: fold: owner needs paths",
	} {
		_, err := Parse([]byte(yaml))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", yaml, err, want)
		}
	}
}
