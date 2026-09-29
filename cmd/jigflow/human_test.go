package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// mergePlaybook is a one-Type Playbook whose ready-to-merge → done Transition
// is a Human Transition with a Gate and an Action that record themselves in
// ran.log, and a T-1 already in ready-to-merge.
func mergePlaybook(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: merge\n")
	p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
statuses: [in-review, ready-to-merge, done]
initial: [in-review]
final: [done]
bindings:
  in-review: code-review
transitions:
  - from: in-review
    to: ready-to-merge
  - from: ready-to-merge
    to: done
    human: true
    gates:
      - {name: lint, cmd: sh record.sh gate}
    actions:
      - {name: commit, cmd: sh record.sh action}
`)
	writeSkill(p, "code-review", true)
	p.Write("record.sh", record)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	p.MustRun("move", "T-1", "ready-to-merge")
	return p
}

func TestAnAgentSessionCanOnlyProposeAHumanTransition(t *testing.T) {
	p := mergePlaybook(t)
	before := p.Read(".jigflow/state/T-1.md")

	r := p.RunInSession("A", "move", "T-1", "done")
	if r.ExitCode != 1 {
		t.Fatalf("an agent's Human Transition exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	want := `T-1: "ready-to-merge" → "done" is a Human Transition. An agent can only propose it`
	if !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\n%s", after)
	}
	assertNothingRan(t, p)
}

// An agent that shells out to jfl move finds the way to ask the person: the
// refusal names jfl's MCP tool that asks them in their client, and where the
// Transition waits for them otherwise.
func TestAnAgentSessionsRefusedHumanTransitionNamesTheToolThatAsksThePerson(t *testing.T) {
	p := mergePlaybook(t)

	r := p.RunInSession("A", "move", "T-1", "done")
	for _, want := range []string{
		"An agent can only propose it, or ask the person for it: if jfl's MCP tools include approve, the move tool asks them in a form only they see.",
		"Otherwise tell the person it waits for them: jfl move T-1 done in a terminal, or the Dashboard (jfl ui).",
	} {
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("refusal: exit %d, stderr %q; want it to contain %q", r.ExitCode, r.Stderr, want)
		}
	}
}

// A Transition the Playbook requires the Dashboard for isn't asked about in
// the agent's client, so its refusal names only the Dashboard.
func TestAnAgentSessionsRefusedDashboardTransitionNamesOnlyTheDashboard(t *testing.T) {
	p := dashboardMergePlaybook(t)

	r := p.RunInSession("A", "move", "T-1", "done")
	if want := "An agent can only propose it, and the Playbook requires making it in the Dashboard: tell the person it waits for them there (jfl ui)."; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal: exit %d, stderr %q; want it to contain %q", r.ExitCode, r.Stderr, want)
	}
	if strings.Contains(r.Stderr, "move tool") {
		t.Errorf("the refusal of a Dashboard-only Transition shouldn't name the move tool: %q", r.Stderr)
	}
}

// assertNothingRan fails if a Gate or Action recorded itself in ran.log.
func assertNothingRan(t *testing.T, p *clitest.Project) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(p.Dir, "ran.log")); err == nil {
		t.Errorf("Gates or Actions ran although the move was refused: %q", p.Read("ran.log"))
	}
}

func TestAHumanTransitionWithoutAnInteractiveTerminalIsRefused(t *testing.T) {
	p := mergePlaybook(t)
	before := p.Read(".jigflow/state/T-1.md")

	r := p.Run("move", "T-1", "done")
	if r.ExitCode != 1 {
		t.Fatalf("a Human Transition without a TTY exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	want := `T-1: "ready-to-merge" → "done" is a Human Transition and needs confirming in an interactive terminal`
	if !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\n%s", after)
	}
	assertNothingRan(t, p)
}

func TestAHumanTransitionInATerminalAsksForConfirmationAndAppliesItOnYes(t *testing.T) {
	p := mergePlaybook(t)

	term := p.StartInTerminal("move", "T-1", "done")
	term.Expect(`T-1 "Add login page": "ready-to-merge" → "done" is a Human Transition. Make it? [y/N] `)
	term.Type("y\n")
	r := term.Wait()
	if r.ExitCode != 0 {
		t.Fatalf("a confirmed Human Transition exited %d, want 0; terminal:\n%s", r.ExitCode, r.Output)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "done" {
		t.Errorf("status = %q, want done", got)
	}
	if got := p.Read("ran.log"); got != "gate\naction\n" {
		t.Errorf("ran.log = %q, want the Gate then the Action", got)
	}
}

func TestAHumanTransitionInATerminalIsNotAppliedUnlessTheAnswerIsYes(t *testing.T) {
	for name, answer := range map[string]string{"no": "n\n", "just Enter": "\n", "anything else": "sure\n"} {
		t.Run(name, func(t *testing.T) {
			p := mergePlaybook(t)
			before := p.Read(".jigflow/state/T-1.md")

			term := p.StartInTerminal("move", "T-1", "done")
			term.Expect("[y/N] ")
			term.Type(answer)
			r := term.Wait()
			if r.ExitCode != 1 {
				t.Fatalf("an unconfirmed Human Transition exited %d, want 1; terminal:\n%s", r.ExitCode, r.Output)
			}
			if want := "T-1: not moved: the Human Transition wasn't confirmed"; !strings.Contains(r.Output, want) {
				t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
			}
			if after := p.Read(".jigflow/state/T-1.md"); after != before {
				t.Errorf("an unconfirmed move changed the Artifact file:\n%s", after)
			}
			assertNothingRan(t, p)
		})
	}
}

func TestAnAgentSessionInATerminalIsRefusedWithoutBeingAsked(t *testing.T) {
	p := mergePlaybook(t)

	r := p.StartInTerminalInSession("A", "move", "T-1", "done").Wait()
	if r.ExitCode != 1 {
		t.Fatalf("an agent's Human Transition in a terminal exited %d, want 1; terminal:\n%s", r.ExitCode, r.Output)
	}
	if want := "An agent can only propose it"; !strings.Contains(r.Output, want) {
		t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
	}
	if strings.Contains(r.Output, "[y/N]") {
		t.Errorf("an agent session was asked to confirm:\n%s", r.Output)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "ready-to-merge" {
		t.Errorf("status = %q, want ready-to-merge", got)
	}
	assertNothingRan(t, p)
}

func TestAPersonMakesAnOrdinaryTransitionWithoutBeingAsked(t *testing.T) {
	p := mergePlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add logout")

	r := p.StartInTerminal("move", "T-2", "ready-to-merge").Wait()
	if r.ExitCode != 0 {
		t.Fatalf("an ordinary Transition in a terminal exited %d, want 0; terminal:\n%s", r.ExitCode, r.Output)
	}
	if strings.Contains(r.Output, "[y/N]") {
		t.Errorf("an ordinary Transition asked for confirmation:\n%s", r.Output)
	}
}
