package kit

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

type journalRes struct {
	Outcome  string  `json:"outcome"`
	Error    *string `json:"error"`
	Caught   string  `json:"caught"`
	Recovery *string `json:"recovery"`
}

func testJournal(out *bytes.Buffer) *Journal[journalRes] {
	return NewJournal(out, journalRes{}, JournalRules[journalRes]{
		Error:  func(r *journalRes, err error) { r.Error = Strp(err.Error()) },
		Caught: func(r *journalRes, inFlight, _ string) { r.Caught = inFlight },
		Close: func(r *journalRes, signalled bool, recovery string) any {
			r.Outcome = OutcomeDone
			if signalled {
				r.Outcome, r.Recovery = OutcomeInterrupted, Strp(recovery)
			}
			return r
		},
	})
}

func readJournal(t *testing.T, out *bytes.Buffer) []journalRes {
	t.Helper()
	var all []journalRes
	dec := json.NewDecoder(out)
	for dec.More() {
		var r journalRes
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	return all
}

func TestJournalWritesOnceWhoeverComesFirst(t *testing.T) {
	var out bytes.Buffer
	j := testJournal(&out)
	j.Start("b")
	j.Interrupted("", "undo it")
	j.Finish()
	all := readJournal(t, &out)
	if len(all) != 1 {
		t.Fatalf("%d objects, want 1", len(all))
	}
	if r := all[0]; r.Outcome != OutcomeInterrupted || r.Caught != "b" || Deref(r.Recovery) != "undo it" {
		t.Fatalf("%+v", r)
	}
}

func TestJournalCatchesTheHandlersStepWhenNoneStarted(t *testing.T) {
	var out bytes.Buffer
	j := testJournal(&out)
	j.Start("a")
	j.Settle("a", func(*journalRes) {})
	j.Interrupted("rebase", "")
	if r := readJournal(t, &out)[0]; r.Caught != "rebase" {
		t.Fatalf("caught %q, want the handler's", r.Caught)
	}
}

func TestJournalSettleLeavesAnotherStepInFlight(t *testing.T) {
	var out bytes.Buffer
	j := testJournal(&out)
	j.Start("a")
	j.Settle("b", func(*journalRes) {})
	j.Interrupted("", "")
	if r := readJournal(t, &out)[0]; r.Caught != "a" {
		t.Fatalf("caught %q, want a", r.Caught)
	}
}

func TestJournalErrorAfterBeginIsNotTheRuns(t *testing.T) {
	var out bytes.Buffer
	j := testJournal(&out)
	j.Begin(nil)
	j.Fail(errors.New("step broke"))
	if r := readJournal(t, &out)[0]; r.Error != nil || r.Outcome != OutcomeDone {
		t.Fatalf("%+v", r)
	}
	out.Reset()
	j = testJournal(&out)
	j.Fail(errors.New("no repo"))
	if r := readJournal(t, &out)[0]; Deref(r.Error) != "no repo" {
		t.Fatalf("%+v", r)
	}
}

func TestJournalNilRecordsNothing(t *testing.T) {
	var j *Journal[journalRes]
	j.Start("a")
	j.Settle("a", func(*journalRes) { t.Fatal("edited a nil journal") })
	j.Edit(func(*journalRes) { t.Fatal("edited a nil journal") })
	j.Begin(nil)
	j.Fail(errors.New("x"))
	j.Interrupted("", "")
	j.Park()
}

func TestJournalSnapshotReadsACopyOutsideTheLock(t *testing.T) {
	var out bytes.Buffer
	j := NewJournal(&out, journalRes{}, JournalRules[journalRes]{
		Snapshot: func(r journalRes, signalled bool, _ string) any {
			r.Outcome = "returned"
			if signalled {
				r.Outcome = OutcomeInterrupted
			}
			return r
		},
	})
	j.Lock()
	j.Res.Caught = "x"
	j.Unlock()
	j.Finish()
	j.Interrupted("", "")
	all := readJournal(t, &out)
	if len(all) != 1 || all[0].Outcome != "returned" || all[0].Caught != "x" {
		t.Fatalf("%+v", all)
	}
}
