package provision

import "testing"

// A nil journal, the run without --json, records nothing and writes nothing.
func TestNilJournalsRecordNothing(t *testing.T) {
	t.Parallel()
	var j *CreateJournal
	j.Finish()
	j.Interrupted("", "")
}
