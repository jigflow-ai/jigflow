package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"golang.org/x/term"
)

// errNoTerminal is returned by confirm when there is nobody to ask.
var errNoTerminal = errors.New("stdin isn't an interactive terminal")

// confirm asks the person at the terminal the question, on stderr, and
// reports whether they answered y or yes. It refuses when stdin isn't an
// interactive terminal, since then there is nobody to ask (ADR 0003).
func (e *env) confirm(question string) (bool, error) {
	if !e.interactive() {
		return false, errNoTerminal
	}
	fmt.Fprintf(e.stderr, "%s [y/N] ", question)
	line, err := bufio.NewReader(e.stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("no answer to the confirmation (%v)", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// interactive reports whether stdin is an interactive terminal: whether
// someone is there to read and answer.
func (e *env) interactive() bool {
	return e.stdin != nil && term.IsTerminal(int(e.stdin.Fd()))
}

// confirmHuman asks the person at the terminal to confirm the Human
// Transition of a to the Status to.
func (e *env) confirmHuman(a engine.Artifact, to string) error {
	ok, err := e.confirm(fmt.Sprintf("%s %q: %q → %q is a Human Transition. Make it?", a.ID, a.Title, a.Status, to))
	if errors.Is(err, errNoTerminal) {
		return fmt.Errorf("%s: %q → %q is a Human Transition and needs confirming in an interactive terminal, but stdin isn't one", a.ID, a.Status, to)
	}
	if err != nil {
		return fmt.Errorf("%s: not moved: %v", a.ID, err)
	}
	if !ok {
		return fmt.Errorf("%s: not moved: the Human Transition wasn't confirmed", a.ID)
	}
	return nil
}
