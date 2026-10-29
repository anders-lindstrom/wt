package refpins

import (
	"slices"
	"testing"
	"time"
)

// A run id names a folder under refs/wt-swept/ that purge and restore act
// on whole: anything not shaped like one wt minted, or a caller chose within
// the same shape, is never read as a run.
func TestRunIDPattern(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]bool{
		"20260930T091500Z-3f2a":                              true,
		"20260930T091500Z-0123456789abcdef0123456789abcdef":  true,
		"20260930T091500Z-3f2":                               false, // under four hex digits
		"20260930T091500Z-0123456789abcdef0123456789abcdef0": false, // over 32
		"20260930T091500Z-3F2A":                              false, // upper case
		"20260930T091500Z-3f2g":                              false, // not hex
		"20260930T091500-3f2a":                               false, // no Z
		"20260930T091500Z3f2a":                               false, // no dash
		"2026930T091500Z-3f2a":                               false, // a short date
		"20260930T091500Z-3f2a\n":                            false,
		" 20260930T091500Z-3f2a":                             false,
		"20260930T091500Z-3f2a/heads":                        false,
		"../20260930T091500Z-3f2a":                           false,
		"":                                                   false,
	} {
		if got := RunIDPattern.MatchString(id); got != want {
			t.Errorf("RunIDPattern.MatchString(%q) = %v, want %v", id, got, want)
		}
	}
}

// A run's age comes from its id when it has no meta; an id in the right shape
// naming no real second has no time rather than a wrong one.
func TestRunTime(t *testing.T) {
	t.Parallel()
	got, ok := runTime("20260930T091500Z-3f2a")
	if want := time.Date(2026, 9, 30, 9, 15, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("runTime = %v %v, want %v true", got, ok, want)
	}
	for _, id := range []string{"20261332T000000Z-3f2a", "20260930T251500Z-3f2a", "not-a-run"} {
		if got, ok := runTime(id); ok {
			t.Errorf("runTime(%q) = %v, want no time", id, got)
		}
	}
}

func TestNewRunIDIsARunIDInUTC(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 30, 11, 15, 0, 0, time.FixedZone("CEST", 2*3600))
	id := NewRunID(at)
	if !RunIDPattern.MatchString(id) {
		t.Fatalf("NewRunID minted %q, which RunIDPattern refuses", id)
	}
	if got, _ := runTime(id); !got.Equal(at) {
		t.Errorf("NewRunID(%v) = %q, which reads back as %v", at, id, got)
	}
}

// parsePin decides which refs under refs/wt-swept/ are pins a restore puts
// back and a purge deletes: only what wt wrote, never the meta, never a ref
// someone else put there.
func TestParsePin(t *testing.T) {
	t.Parallel()
	const run = "20260930T091500Z-3f2a"
	for _, c := range []struct {
		pin        string
		ok         bool
		kind, name string
	}{
		{SweptPrefix + run + "/heads/main", true, RefBranch, "main"},
		{SweptPrefix + run + "/heads/fix/deep/name", true, RefBranch, "fix/deep/name"},
		{SweptPrefix + run + "/tags/v1.0", true, RefTag, "v1.0"},
		{SweptPrefix + run + "/remote-heads/fix/a", true, RefRemoteBranch, "fix/a"},
		{SweptPrefix + run + "/remote-tags/v2", true, RefRemoteTag, "v2"},
		{SweptPrefix + run + "/meta", false, "", ""},
		{SweptPrefix + run + "/heads/", false, "", ""},
		{SweptPrefix + run + "/heads", false, "", ""},
		{SweptPrefix + run + "/stash/x", false, "", ""},
		{SweptPrefix + run + "/HEADS/x", false, "", ""},
		{SweptPrefix + "not-a-run/heads/x", false, "", ""},
		{SweptPrefix + "20260930T091500Z-3F2A/heads/x", false, "", ""},
		{SweptPrefix + "heads/x", false, "", ""},
		{"refs/heads/" + run + "/heads/x", false, "", ""},
		{"refs/wt-quarantine/" + run + "/heads/x", false, "", ""},
	} {
		got, ok := parsePin(c.pin)
		if ok != c.ok {
			t.Errorf("parsePin(%q) ok = %v, want %v (%+v)", c.pin, ok, c.ok, got)
			continue
		}
		if ok && (got.RunID != run || got.Kind != c.kind || got.Name != c.name || got.Pin != c.pin) {
			t.Errorf("parsePin(%q) = %+v, want run %s kind %s name %s", c.pin, got, run, c.kind, c.name)
		}
	}
}

// Where a pin is written and how it is read back must agree for every
// kind, or a restore puts a tag back as a branch, or a branch here back on
// origin.
func TestPinOfReadsBackAsTheRefItPinned(t *testing.T) {
	t.Parallel()
	const run = "20260930T091500Z-3f2a"
	for _, c := range []struct {
		kind, name, ref, id string
		remote              bool
	}{
		{RefBranch, "fix/a", "refs/heads/fix/a", "refs/heads/fix/a", false},
		{RefTag, "v1", "refs/tags/v1", "refs/tags/v1", false},
		{RefRemoteBranch, "fix/a", "refs/heads/fix/a", "origin:refs/heads/fix/a", true},
		{RefRemoteTag, "v1", "refs/tags/v1", "origin:refs/tags/v1", true},
	} {
		pin := PinOf(run, c.kind, c.name)
		s, ok := parsePin(pin)
		if !ok || s.Kind != c.kind || s.Name != c.name {
			t.Errorf("%s %s: pin %s reads back as %+v %v", c.kind, c.name, pin, s, ok)
			continue
		}
		if s.Ref() != c.ref || s.ID() != c.id || RefID(c.kind, c.name) != c.id {
			t.Errorf("%s %s: ref %s id %s, want %s %s", c.kind, c.name, s.Ref(), s.ID(), c.ref, c.id)
		}
		if RemoteKind(c.kind) != c.remote {
			t.Errorf("RemoteKind(%s) = %v", c.kind, RemoteKind(c.kind))
		}
	}
}

func TestMetaRun(t *testing.T) {
	t.Parallel()
	const run = "20260930T091500Z-3f2a"
	if id, ok := metaRun(MetaRef(run)); !ok || id != run {
		t.Errorf("metaRun(MetaRef) = %q %v", id, ok)
	}
	for _, ref := range []string{
		SweptPrefix + run + "/heads/meta",
		SweptPrefix + run + "/meta/x",
		SweptPrefix + "not-a-run/meta",
		SweptPrefix + run,
		"refs/heads/" + run + "/meta",
	} {
		if id, ok := metaRun(ref); ok {
			t.Errorf("metaRun(%q) = %q, want no run", ref, id)
		}
	}
}

// parseObject gives the date and subject a purge or sweep plan shows for
// each ref: the committer's time, not the author's, and a tag's own date
// and target.
func TestParseObject(t *testing.T) {
	t.Parallel()
	commit := "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n" +
		"parent 1111111111111111111111111111111111111111\n" +
		"author A U <a@example.com> 1700000000 +0000\n" +
		"committer C O <c@example.com> 1759220000 +0200\n" +
		"\n\n   the subject  \n\nthe body\n"
	tag := "object 2222222222222222222222222222222222222222\n" +
		"type commit\ntag v1\n" +
		"tagger T <t@example.com> 1759220001 -0500\n" +
		"\nrelease one\n"
	for _, c := range []struct {
		typ, body string
		want      ObjectInfo
	}{
		{"commit", commit, ObjectInfo{Type: "commit", Time: 1759220000, Subject: "the subject"}},
		{"tag", tag, ObjectInfo{Type: "tag", Target: "2222222222222222222222222222222222222222",
			Time: 1759220001, Subject: "release one"}},
		{"commit", "tree 4b82\ncommitter C <c@example.com> 5 +0000\n", ObjectInfo{Type: "commit", Time: 5}},
		{"tree", "100644 blob f\tname", ObjectInfo{Type: "tree"}},
		{"blob", "object 3333\n\nsubject", ObjectInfo{Type: "blob"}},
	} {
		if got := parseObject(c.typ, []byte(c.body)); got != c.want {
			t.Errorf("parseObject(%s, %q) = %+v, want %+v", c.typ, c.body, got, c.want)
		}
	}
}

func TestIdentTime(t *testing.T) {
	t.Parallel()
	for ident, want := range map[string]int64{
		"Name <e@example.com> 1759220000 +0200": 1759220000,
		"A > B <e@example.com> 7 +0000":         7,
		"Name <e@example.com>":                  0,
		"Name <e@example.com> soon +0000":       0,
		"Name e@example.com 1759220000 +0200":   0,
		"":                                      0,
	} {
		if got := identTime(ident); got != want {
			t.Errorf("identTime(%q) = %d, want %d", ident, got, want)
		}
	}
}

func TestRestoreFromRunNamesOneBranch(t *testing.T) {
	t.Parallel()
	got := RestoreFromRun("20260930T091500Z-3f2a", "fix/a")
	want := []string{"wt", "refs", "restore", "20260930T091500Z-3f2a", "--only", "refs/heads/fix/a", "--yes"}
	if !slices.Equal(got, want) {
		t.Errorf("RestoreFromRun = %q, want %q", got, want)
	}
}
