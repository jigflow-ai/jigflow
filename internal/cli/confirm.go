package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"golang.org/x/term"
)

// confirmHuman asks the person at the terminal to confirm the Human
// Transition of a to the Status to. It refuses when stdin isn't an
// interactive terminal, since then there is nobody to ask (ADR 0003).
func (e *env) confirmHuman(a engine.Artifact, to string) error {
	if e.stdin == nil || !term.IsTerminal(int(e.stdin.Fd())) {
		return fmt.Errorf("%s: %q → %q is a Human Transition and needs confirming in an interactive terminal, but stdin isn't one", a.ID, a.Status, to)
	}
	fmt.Fprintf(e.stderr, "%s %q: %q → %q is a Human Transition. Make it? [y/N] ", a.ID, a.Title, a.Status, to)
	line, err := bufio.NewReader(e.stdin).ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("%s: not moved: no answer to the confirmation (%v)", a.ID, err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return fmt.Errorf("%s: not moved: the Human Transition wasn't confirmed", a.ID)
}
