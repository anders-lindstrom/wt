package wtsync

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseEachCommitAndNeeds(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`
defer:
  - run: ./gen.sh
    needs: [docker]
each_commit:
  - run: ./check.sh
    paths: ["**/*.java"]
    needs: [docker]
  - run: ./lint.sh
`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Defer[0].Needs, []string{"docker"}) {
		t.Errorf("defer needs %v", cfg.Defer[0].Needs)
	}
	if len(cfg.EachCommit) != 2 {
		t.Fatalf("each_commit %+v", cfg.EachCommit)
	}
	c := cfg.EachCommit[0]
	if c.Run != "./check.sh" || !reflect.DeepEqual(c.Paths, []string{"**/*.java"}) || !reflect.DeepEqual(c.Needs, []string{"docker"}) {
		t.Errorf("each_commit[0] %+v", c)
	}
	if cfg.EachCommit[1].Run != "./lint.sh" || len(cfg.EachCommit[1].Paths) != 0 {
		t.Errorf("each_commit[1] %+v", cfg.EachCommit[1])
	}
}

func TestParseRefusesAnUnknownNeed(t *testing.T) {
	t.Parallel()
	for _, yaml := range []string{
		"defer:\n  - run: ./gen.sh\n    needs: [podman]\n",
		"each_commit:\n  - run: ./check.sh\n    needs: [podman]\n",
	} {
		_, err := Parse([]byte(yaml))
		if err == nil || !strings.Contains(err.Error(), `unknown need "podman"`) || !strings.Contains(err.Error(), "docker") {
			t.Errorf("%q: err %v, want an unknown need naming the known ones", yaml, err)
		}
	}
}

func TestParseRefusesAnEachCommitStepWithoutRun(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("each_commit:\n  - paths: [a.txt]\n"))
	if err == nil || !strings.Contains(err.Error(), "each_commit[0] has no run") {
		t.Fatalf("err %v", err)
	}
}

func TestParseRefusesACommitOnAnEachCommitStep(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("each_commit:\n  - run: ./check.sh\n    commit: fix\n"))
	if err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("err %v", err)
	}
}
