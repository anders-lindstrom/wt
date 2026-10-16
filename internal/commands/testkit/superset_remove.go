package testkit

import "strings"

// Deleted is a fake superset answer that deleted one workspace.
const Deleted = `echo '{"deleted":["w1"],"warnings":[]}'`

// SupersetLines is every output line about Superset. Paths are left out of
// the question: they carry the test's own name.
func SupersetLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		var words []string
		for _, word := range strings.Fields(line) {
			if !strings.Contains(word, "/") {
				words = append(words, word)
			}
		}
		if strings.Contains(strings.ToLower(strings.Join(words, " ")), "superset") {
			lines = append(lines, line)
		}
	}
	return lines
}
