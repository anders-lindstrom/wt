package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// prompter asks a command's questions. Build one per command and ask every
// question through it: a bufio.Reader buffers past its own line, so a reader
// built per question eats the answers typed ahead for the next one.
type prompter struct {
	r   *bufio.Reader
	out io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{r: bufio.NewReader(in), out: out}
}

// yesNo prints question with its [y/N] or [Y/n] and reads one line. An empty
// line takes def. EOF on a terminal is ^D: the user declined rather than
// answered, whatever the default.
func (p *prompter) yesNo(question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	_, _ = fmt.Fprintf(p.out, "%s %s ", question, hint)
	line, err := p.r.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}
