package main_test

import (
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// autopilot runs one autopilot step, `jfl next --autopilot`, as agent
// session A and fails the test unless it exits 0.
func autopilot(t *testing.T, p *clitest.Project) (first, all string) {
	t.Helper()
	r := p.RunInSession("A", "next", "--autopilot")
	if r.ExitCode != 0 {
		t.Fatalf("next --autopilot exited %d\nstdout: %s\nstderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	return firstLine(r.Stdout), r.Stdout
}

func TestAutopilotStopsWhenNothingIsLeftForAnAgentAndSaysWhatWaitsForAPerson(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")

	if first, _ := autopilot(t, p); first != `run /implement on T-1 "Login page"` {
		t.Fatalf("first autopilot step = %q, want the pick of next", first)
	}
	agentMove(t, p, "T-1", "in-progress", 0)
	if first, _ := autopilot(t, p); first != `run /implement on T-1 "Login page"` {
		t.Fatalf("second autopilot step = %q, want T-1 again, now in-progress", first)
	}
	agentMove(t, p, "T-1", "in-review", 0)

	first, all := autopilot(t, p)
	if first != "autopilot stopped: nothing for an agent to do" {
		t.Errorf("autopilot = %q, want it stopped with nothing to do", first)
	}
	if want := "Waiting for a person:\n  T-1: \"in-review\" has no Binding, so it's human work\n"; !strings.Contains(all, want) {
		t.Errorf("autopilot output =\n%s\nwant %q", all, want)
	}
}

func TestAutopilotStopsWhenTheSkillItHandedOutDidNotMoveTheArtifactOn(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.MustRun("create", "Ticket", "--title", "Signup page")
	autopilot(t, p)
	// The agent runs /implement on T-1 but never moves it.

	first, all := autopilot(t, p)
	want := `autopilot stopped: /implement ran on T-1 "Login page", but it is still in "ready-for-agent"; nothing moved it on`
	if first != want {
		t.Errorf("autopilot = %q, want %q", first, want)
	}
	if strings.Contains(all, "run /") {
		t.Errorf("a stopped autopilot handed out more work:\n%s", all)
	}
	if focus := focusOf(t, p, "A"); focus != "" {
		t.Errorf("a stopped autopilot left Focus %q, want none", focus)
	}

	// Stopping ends the run: the next autopilot starts afresh.
	if first, _ := autopilot(t, p); first != `run /implement on T-1 "Login page"` {
		t.Errorf("a new autopilot run = %q, want T-1 handed out again", first)
	}
}

// shipPlaybook is a one-Type Playbook where /implement works on open Tickets,
// open → built is gated by a check that passes once pass.flag exists, and
// built → done is a Human Transition.
func shipPlaybook(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: ship\n")
	p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
statuses: [open, built, done]
initial: [open]
final: [done]
bindings:
  open: implement
  built: implement
transitions:
  - from: open
    to: built
    gates:
      - {name: tests, cmd: test -f pass.flag}
  - from: built
    to: done
    human: true
`)
	writeSkill(p, "implement", true)
	p.MustRun("create", "Ticket", "--title", "Login page")
	return p
}

func TestAutopilotStopsAtAHumanTransitionItWasRefused(t *testing.T) {
	p := shipPlaybook(t)
	p.Write("pass.flag", "")
	autopilot(t, p)
	agentMove(t, p, "T-1", "built", 0)
	autopilot(t, p)
	refusal := agentMove(t, p, "T-1", "done", 1)

	first, _ := autopilot(t, p)
	want := "autopilot stopped: jfl move T-1 done was refused: " + strings.TrimSpace(strings.TrimPrefix(refusal.Stderr, "jfl move: "))
	if first != want {
		t.Errorf("autopilot = %q, want %q", first, want)
	}
	if !strings.Contains(first, `"built" → "done" is a Human Transition`) {
		t.Errorf("autopilot = %q, want it to say the Human Transition is waiting", first)
	}
}

func TestAutopilotKeepsGoingWhenARefusedMoveWasThenMade(t *testing.T) {
	p := shipPlaybook(t)
	autopilot(t, p)
	agentMove(t, p, "T-1", "built", 1) // the tests Gate fails
	p.Write("pass.flag", "")           // the Skill fixes the work
	agentMove(t, p, "T-1", "built", 0)

	if first, _ := autopilot(t, p); first != `run /implement on T-1 "Login page"` {
		t.Errorf("autopilot = %q, want it to go on with T-1, now built", first)
	}
}

func TestAutopilotStoppedByAPendingProposalListsItAsWaitingForAPerson(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	if first, _ := autopilot(t, p); first != `run /to-tickets on S-1 "Password reset by email"` {
		t.Fatalf("autopilot = %q, want /to-tickets on S-1", first)
	}
	p.Write("breakdown.yaml", breakdown)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}

	first, all := autopilot(t, p)
	if first != "autopilot stopped: nothing for an agent to do" {
		t.Errorf("autopilot = %q, want it stopped with nothing to do", first)
	}
	want := "Waiting for a person:\n" +
		"  S-1: waiting on a pending Proposal (P-1)\n" +
		"  P-1: a pending Proposal to approve or reject: break S-1 into 3 tickets\n"
	if !strings.Contains(all, want) {
		t.Errorf("autopilot output =\n%s\nwant %q", all, want)
	}
}

func TestAutopilotIsForAgentSessions(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")

	r := p.Run("next", "--autopilot")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "autopilot is for agent sessions") {
		t.Errorf("a person's autopilot: exit %d, stderr %q; want it refused", r.ExitCode, r.Stderr)
	}
}

func TestThePublishedRouterAndAgentsMdTellAnAgentHowToRunAutopilot(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("publish", "claude-code")
	p.MustRun("publish", "agents-md")

	_, router := skillFrontmatter(t, p.Read(".claude/skills/jigflow/SKILL.md"))
	for name, body := range map[string]string{"router Skill": router, "AGENTS.md": p.Read("AGENTS.md")} {
		for _, want := range []string{"`jfl next --autopilot`", "autopilot stopped", "what is waiting for a person"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s =\n%s\nwant %q", name, body, want)
			}
		}
	}
}
