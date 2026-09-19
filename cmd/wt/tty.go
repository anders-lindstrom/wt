package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// canAsk reports whether cmd has anyone to answer a prompt: its input is a
// terminal. It checks the reader the prompt will read, not os.Stdin.
func canAsk(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && isTerminal(f)
}

// isTerminal reports whether f is an interactive terminal, which is the whole
// of the question "is there anyone here to answer a prompt".
//
// This asks the kernel rather than reading the file mode. The usual
// ModeCharDevice test is wrong in exactly the case that matters: /dev/null is a
// character device, so a script or an agent redirecting stdin from it would be
// asked a question with nobody there to answer.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// terminalWidth is w's column count when w is a terminal, and 0 otherwise.
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(f) {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return width
}

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

// line reads one answer without printing a question; the caller has already
// written whatever it is asking. EOF comes back as an error.
func (p *prompter) line() (string, error) {
	s, err := p.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}
