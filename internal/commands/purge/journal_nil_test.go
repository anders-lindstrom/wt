package purge

import (
	"errors"
	"testing"
)

// A nil journal, the run without --json, records nothing and writes nothing.
func TestNilJournalsRecordNothing(t *testing.T) {
	t.Parallel()
	var j *RefsPurgeJournal
	j.Fail(errors.New("x"))
	j.Finish()
	j.Interrupted("", "")
}
