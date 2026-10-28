package retire

import (
	"errors"
	"testing"
)

// A nil journal, the run without --json, records nothing and writes nothing.
func TestNilJournalsRecordNothing(t *testing.T) {
	t.Parallel()
	var sweep *SweepJournal
	sweep.Fail(errors.New("x"))
	sweep.Finish()
	sweep.Interrupted("", "")
	var remove *RemoveJournal
	remove.finish(RemoveResult{}, errors.New("x"))
	remove.Interrupted("", "")
}
