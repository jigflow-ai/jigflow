package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// writeSkill declares the Skill name in the project, bound to Statuses, with
// the given changes flag.
func writeSkill(p *clitest.Project, name string, changes bool) {
	p.Write(".jigflow/skills/"+name+"/SKILL.md", fmt.Sprintf(`---
changes: %t
invocation: bound
---
Work on the Artifact in Focus.
`, changes))
}

func TestCheckPassesAValidPlaybook(t *testing.T) {
	p := ticketPlaybook(t)
	r := p.MustRun("check")
	if want := `Playbook "skeleton": no problems`; firstLine(r.Stdout) != want {
		t.Errorf("check = %q, want %q", firstLine(r.Stdout), want)
	}
}

func TestCheckReportsABindingToAMissingSkill(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "in-progress: implement", "in-progress: implemnt", 1))

	r := p.Run("check")
	want := `.jigflow/types/ticket.yaml: the Binding of "in-progress" names Skill "implemnt", which the Playbook doesn't declare`
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
}

func TestCheckReportsASkillThatDoesNotDeclareChangesOrItsInvocationMode(t *testing.T) {
	const skill = ".jigflow/skills/implement/SKILL.md"
	for name, c := range map[string]struct{ frontmatter, want string }{
		"no changes":              {"invocation: bound\n", skill + ": a Skill must declare changes: true or false"},
		"no Invocation Mode":      {"changes: true\n", skill + ": a Skill must declare its Invocation Mode (invocation: user, agent or bound)"},
		"unknown Invocation Mode": {"changes: true\ninvocation: auto\n", skill + `: unknown Invocation Mode "auto" (want user, agent or bound)`},
		"no frontmatter":          {"", skill + ": a Skill must declare changes: true or false"},
		"misspelt field":          {"changes: true\ninvocation: bound\nchnages: false\n", "chnages"},
	} {
		t.Run(name, func(t *testing.T) {
			p := ticketPlaybook(t)
			body := "Work on the Artifact in Focus.\n"
			if c.frontmatter != "" {
				body = "---\n" + c.frontmatter + "---\n" + body
			}
			p.Write(skill, body)

			r := p.Run("check")
			if r.ExitCode != 1 || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, c.want)
			}
		})
	}
}

func TestEveryInvocationModeIsValid(t *testing.T) {
	for _, mode := range []string{"user", "agent", "bound"} {
		p := ticketPlaybook(t)
		p.Write(".jigflow/skills/grill/SKILL.md", "---\nchanges: false\ninvocation: "+mode+"\n---\nAsk questions.\n")
		if r := p.Run("check"); r.ExitCode != 0 {
			t.Errorf("invocation %s: check exited %d, stderr %q", mode, r.ExitCode, r.Stderr)
		}
	}
}

func TestCheckReportsAStatusNoTransitionReaches(t *testing.T) {
	p := ticketPlaybook(t)
	ticket := strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "in-review, done]", "in-review, blocked, done]", 1)
	p.Write(".jigflow/types/ticket.yaml", ticket+"  - from: blocked\n    to: in-progress\n")

	r := p.Run("check")
	want := `.jigflow/types/ticket.yaml: Status "blocked" can't be reached from an initial Status`
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
}

func TestCheckReportsAStatusWithNoWayOutThatIsNotFinal(t *testing.T) {
	p := ticketPlaybook(t)
	ticket := strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "in-review, done]", "in-review, on-hold, done]", 1)
	p.Write(".jigflow/types/ticket.yaml", ticket+"  - from: in-review\n    to: on-hold\n")

	r := p.Run("check")
	want := `.jigflow/types/ticket.yaml: Status "on-hold" has no Transition out and isn't final`
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
}

func TestCheckReportsEveryProblemAtOnceIncludingUndeclaredLinks(t *testing.T) {
	p := ticketPlaybook(t)
	ticket := strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "in-progress: implement", "in-progress: implemnt", 1)
	p.Write(".jigflow/types/ticket.yaml", ticket+`readiness:
  ready-for-agent:
    - {kind: linked-all-in, link: depends_on, statuses: [done]}
`)

	r := p.Run("check")
	for _, want := range []string{
		"the Playbook has 2 problems:",
		`.jigflow/types/ticket.yaml: Readiness of "ready-for-agent" refers to Link "depends_on", which a Ticket doesn't declare`,
		`.jigflow/types/ticket.yaml: the Binding of "in-progress" names Skill "implemnt", which the Playbook doesn't declare`,
	} {
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
		}
	}
}

func TestCheckReportsAPersonaThatDoesNotExistAndHasNoFallback(t *testing.T) {
	const skill = ".jigflow/skills/implement/SKILL.md"
	for name, c := range map[string]struct {
		persona string // the Skill's entry for the Persona
		exists  bool   // whether .jigflow/personas/reviewer.md exists
		want    string // the problem, or "" for none
	}{
		"missing, no fallback":   {"{name: reviewer}", false, skill + `: Persona "reviewer" doesn't exist and has no fallback description`},
		"missing, with fallback": {"{name: reviewer, fallback: A senior engineer who reviews for clarity}", false, ""},
		"declared":               {"{name: reviewer}", true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.Write(skill, "---\nchanges: true\ninvocation: bound\npersonas:\n  - "+c.persona+"\n---\nReview as the reviewer.\n")
			if c.exists {
				p.Write(".jigflow/personas/reviewer.md", "A senior engineer who reviews for clarity.\n")
			}

			r := p.Run("check")
			if c.want == "" && r.ExitCode != 0 {
				t.Errorf("check: exit %d, stderr %q; want no problems", r.ExitCode, r.Stderr)
			}
			if c.want != "" && (r.ExitCode != 1 || !strings.Contains(r.Stderr, c.want)) {
				t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, c.want)
			}
		})
	}
}

func TestCheckPassesThePrototypesPocockPlaybook(t *testing.T) {
	p := pocockPlaybook(t)
	if r := p.MustRun("check"); firstLine(r.Stdout) != `Playbook "pocock": no problems` {
		t.Errorf("check = %q, want no problems", firstLine(r.Stdout))
	}
}

func TestCheckRefusesAnInboxThatReachesAChangingSkillWithoutAHumanTransition(t *testing.T) {
	for name, c := range map[string]struct{ from, to string }{
		"in one Transition":    {"{from: needs-triage, to: ready-for-agent, human: true}", "{from: needs-triage, to: ready-for-agent}"},
		"through other Status": {"{from: needs-triage, to: ready-for-agent, human: true}", "{from: needs-info, to: ready-for-agent}"},
	} {
		t.Run(name, func(t *testing.T) {
			p := pocockPlaybook(t)
			p.Write(".jigflow/types/3-issue.yaml", strings.Replace(p.Read(".jigflow/types/3-issue.yaml"), c.from, c.to, 1))

			r := p.Run("check")
			want := `.jigflow/types/3-issue.yaml: Issue Inbox "needs-triage" reaches /implement in "ready-for-agent" without a Human Transition`
			if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
				t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
			}
		})
	}
}

func TestEveryCommandRefusesToRunWithAnInvalidPlaybookAndPrintsItsProblems(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.Write("breakdown.yaml", breakdown)
	p.MustRun("propose", "breakdown.yaml")
	p.Write(".jigflow/types/3-issue.yaml", strings.Replace(p.Read(".jigflow/types/3-issue.yaml"),
		"{from: needs-triage, to: ready-for-agent, human: true}", "{from: needs-triage, to: ready-for-agent}", 1))
	p.Write(".jigflow/skills/code-review/SKILL.md", "---\nchanges: true\n---\nReview it.\n")
	state := map[string]string{}
	for _, f := range []string{"state/S-1.md", "state/T-1.md", "proposals/P-1.yaml"} {
		state[f] = p.Read(".jigflow/" + f)
	}

	for _, args := range [][]string{
		{"check"},
		{"next"},
		{"create", "Issue", "--title", "Token not invalidated after use"},
		{"move", "T-1", "in-progress"},
		{"propose", "breakdown.yaml"},
		{"approve", "P-1"},
		{"reject", "P-1"},
	} {
		r := p.Run(args...)
		if r.ExitCode != 1 {
			t.Errorf("jfl %s exited %d, want 1; stdout %q", strings.Join(args, " "), r.ExitCode, r.Stdout)
		}
		for _, want := range []string{
			"the Playbook has 2 problems:",
			`Issue Inbox "needs-triage" reaches /implement in "ready-for-agent" without a Human Transition`,
			".jigflow/skills/code-review/SKILL.md: a Skill must declare its Invocation Mode",
		} {
			if !strings.Contains(r.Stderr, want) {
				t.Errorf("jfl %s: stderr %q should contain %q", strings.Join(args, " "), r.Stderr, want)
			}
		}
	}
	for f, before := range state {
		if after := p.Read(".jigflow/" + f); after != before {
			t.Errorf("%s changed with an invalid Playbook:\n%s", f, after)
		}
	}
	assertNoArtifact(t, p, "I-1")
}
