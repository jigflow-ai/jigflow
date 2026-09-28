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
// Transition tr of a. In the Dashboard, the person's click is the
// confirmation; one the Playbook requires the Dashboard for is refused
// anywhere else.
func (e *env) confirmHuman(a engine.Artifact, tr engine.Transition) error {
	if e.clicked {
		return nil
	}
	to := tr.To
	if tr.Dashboard {
		return fmt.Errorf("%s and make it there", dashboardOnly(a.ID, tr))
	}
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

// confirmApproval asks the person at the terminal to confirm approving p.
// In the Dashboard, the person's click is the confirmation.
func (e *env) confirmApproval(p engine.Proposal) error {
	if e.clicked {
		return nil
	}
	by := "a person"
	if p.By != "" {
		by = "agent session " + p.By
	}
	ok, err := e.confirm(fmt.Sprintf("%s from %s: %s\n%sApprove all %s as one unit?", p.ID, by, p.Summary, listItems(p), plural(len(p.Items), "change")))
	if errors.Is(err, errNoTerminal) {
		return fmt.Errorf("approving %s needs confirming in an interactive terminal, but stdin isn't one", p.ID)
	}
	if err != nil {
		return fmt.Errorf("%s: not approved: %v", p.ID, err)
	}
	if !ok {
		return fmt.Errorf("%s: not approved: the approval wasn't confirmed", p.ID)
	}
	return nil
}

// dashboardOnly says that the Transition tr of the Artifact id is one the
// Playbook requires making in the Dashboard.
func dashboardOnly(id string, tr engine.Transition) string {
	return fmt.Sprintf("%s: %q → %q is a Human Transition the Playbook requires making in the Dashboard: run jfl ui", id, tr.From, tr.To)
}
