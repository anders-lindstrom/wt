package syncrun

import (
	"errors"
	"testing"
)

// A nil journal, the run without --json, records nothing and writes nothing.
func TestNilJournalsRecordNothing(t *testing.T) {
	t.Parallel()
	var j *RunJournal
	j.Fail(errors.New("x"))
	j.Finish()
	j.Interrupted("", "")
}
