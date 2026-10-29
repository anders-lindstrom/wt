package refs

import (
	"errors"
	"testing"
)

// A nil journal, the run without --json, records nothing and writes nothing.
func TestNilJournalsRecordNothing(t *testing.T) {
	t.Parallel()
	var sweep *Journal
	sweep.Fail(errors.New("x"))
	sweep.Finish()
	sweep.Interrupted("", "")
	var restore *RestoreRefsJournal
	restore.Fail(errors.New("x"))
	restore.Finish()
	restore.Interrupted("", "")
	var purge *PurgeJournal
	purge.Fail(errors.New("x"))
	purge.Finish()
	purge.Interrupted("", "")
}
