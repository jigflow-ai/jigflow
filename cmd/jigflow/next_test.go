package main_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestNextHandsTheBoundSkillForTheFirstEligibleArtifact(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "First")
	p.MustRun("create", "Ticket", "--title", "Second")

	r := p.MustRun("next")
	if first := firstLine(r.Stdout); first != `run /implement on T-1 "First"` {
		t.Errorf("next = %q, want %q", first, `run /implement on T-1 "First"`)
	}
}

func TestNextFollowsTheBindingOfTheArtifactsStatus(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: review\n")
	p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
statuses: [open, in-review, done]
initial: [open]
final: [done]
bindings:
  open: implement
  in-review: code-review
transitions:
  - {from: open, to: in-review}
  - {from: in-review, to: done}
`)
	p.MustRun("create", "Ticket", "--title", "Reviewed")
	p.MustRun("move", "T-1", "in-review")

	if first := firstLine(p.MustRun("next").Stdout); first != `run /code-review on T-1 "Reviewed"` {
		t.Errorf("next = %q, want the in-review Binding", first)
	}
}

func TestNextSkipsFinalArtifactsAndReportsHumanWork(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Shipped")
	p.MustRun("create", "Ticket", "--title", "Awaiting review")
	p.MustRun("create", "Ticket", "--title", "Up next")
	for _, to := range []string{"in-progress", "in-review", "done"} {
		p.MustRun("move", "T-1", to)
	}
	p.MustRun("move", "T-2", "in-progress")
	p.MustRun("move", "T-2", "in-review") // no Binding: human work

	r := p.MustRun("next")
	if first := firstLine(r.Stdout); first != `run /implement on T-3 "Up next"` {
		t.Errorf("next = %q, want T-3", first)
	}
	if !strings.Contains(r.Stdout, `T-2: "in-review" has no Binding, so it's human work`) {
		t.Errorf("next output should report T-2 as human work:\n%s", r.Stdout)
	}
	if strings.Contains(r.Stdout, "T-1") {
		t.Errorf("next output should not mention the final Artifact T-1:\n%s", r.Stdout)
	}
}

func TestNextWithNothingForAnAgentSaysSo(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Awaiting review")
	p.MustRun("move", "T-1", "in-progress")
	p.MustRun("move", "T-1", "in-review")

	r := p.MustRun("next")
	if first := firstLine(r.Stdout); first != "nothing for an agent to do" {
		t.Errorf("next = %q, want %q", first, "nothing for an agent to do")
	}
	if !strings.Contains(r.Stdout, "T-1: \"in-review\" has no Binding, so it's human work") {
		t.Errorf("next output should still report the human work:\n%s", r.Stdout)
	}
}

func TestNextKeepsDeclarationOrderPastNineArtifacts(t *testing.T) {
	p := ticketPlaybook(t)
	for i := 1; i <= 10; i++ {
		p.MustRun("create", "Ticket", "--title", fmt.Sprintf("Ticket %d", i))
	}
	p.MustRun("move", "T-1", "in-progress")
	p.MustRun("move", "T-1", "in-review")

	if first := firstLine(p.MustRun("next").Stdout); first != `run /implement on T-2 "Ticket 2"` {
		t.Errorf("next = %q, want T-2 (T-10 was created later)", first)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
