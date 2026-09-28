package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// humanMove makes the Human Transition of the Artifact id to the Status
// to, as a person confirming it in a terminal.
func humanMove(t *testing.T, p *clitest.Project, id, to string) {
	t.Helper()
	term := p.StartInTerminal("move", id, to)
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("moving %s to %s exited %d; terminal:\n%s", id, to, r.ExitCode, r.Output)
	}
}

func TestAnAgentProposesAPersonaAndOnlyAPersonActivatesIt(t *testing.T) {
	p := ticketPlaybook(t)

	r := p.RunInSession("A", "create", "Persona", "--title", "security-auditor")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, `created PERSONA-1 "security-auditor" in proposed`) {
		t.Fatalf("an agent proposing a Persona: exit %d, stdout %q, stderr %q", r.ExitCode, r.Stdout, r.Stderr)
	}
	r = p.RunInSession("A", "move", "PERSONA-1", "active")
	if want := `"proposed" → "active" is a Human Transition. An agent can only propose it.`; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("an agent activating a Persona: exit %d, stderr %q; want %q", r.ExitCode, r.Stderr, want)
	}

	humanMove(t, p, "PERSONA-1", "active")
	if got := frontmatter(t, p.Read(".jigflow/state/PERSONA-1.md"))["status"]; got != "active" {
		t.Errorf("status = %q, want active", got)
	}
	humanMove(t, p, "PERSONA-1", "retired")
	if got := frontmatter(t, p.Read(".jigflow/state/PERSONA-1.md"))["status"]; got != "retired" {
		t.Errorf("status = %q, want retired", got)
	}
}

func TestCheckRefusesAnArtifactTypeThatClashesWithTheBuiltInPersonaType(t *testing.T) {
	for name, c := range map[string]struct{ file, want string }{
		"by name":   {"name: Persona\nprefix: P\nstatuses: [draft]\ninitial: [draft]\nfinal: [draft]\n", `.jigflow/types/persona.yaml: Artifact Type "Persona" is built in; a Playbook can't declare it`},
		"by prefix": {"name: Role\nprefix: PERSONA\nstatuses: [draft]\ninitial: [draft]\nfinal: [draft]\n", `.jigflow/types/persona.yaml: prefix "PERSONA" is the built-in Persona Type's`},
	} {
		t.Run(name, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.Write(".jigflow/types/persona.yaml", c.file)
			if r := p.Run("check"); r.ExitCode != 1 || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, c.want)
			}
		})
	}
}

func TestAPersonasNameIsItsTitleAndNamesOnePersona(t *testing.T) {
	p := ticketPlaybook(t)
	for title, want := range map[string]string{
		"security auditor": `a Persona's title is its name`,
		"../reviewer":      `a Persona's title is its name`,
	} {
		if r := p.RunInSession("A", "create", "Persona", "--title", title); r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("create Persona %q: exit %d, stderr %q; want %q", title, r.ExitCode, r.Stderr, want)
		}
	}
	p.MustRun("create", "Persona", "--title", "reviewer")
	if r := p.RunInSession("A", "create", "Persona", "--title", "reviewer"); r.ExitCode != 1 || !strings.Contains(r.Stderr, `Persona "reviewer" is already PERSONA-1`) {
		t.Errorf("a second Persona of the same name: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

// describePersona writes the Markdown describing the Persona id into its
// Artifact's body, as an agent or a person may edit any Artifact's body.
func describePersona(p *clitest.Project, id, description string) {
	file := ".jigflow/state/" + id + ".md"
	p.Write(file, p.Read(file)+description)
}

// reviewing is the ticket Playbook with an implement Skill that asks for the
// reviewer Persona, and a reviewer Persona an agent proposed.
func reviewing(t *testing.T) *clitest.Project {
	t.Helper()
	p := ticketPlaybook(t)
	p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\npersonas:\n  - {name: reviewer, fallback: A senior engineer who reviews for clarity.}\n---\nWork on the Ticket in Focus.\n")
	if r := p.RunInSession("A", "create", "Persona", "--title", "reviewer"); r.ExitCode != 0 {
		t.Fatalf("an agent proposing a Persona exited %d: %s", r.ExitCode, r.Stderr)
	}
	describePersona(p, "PERSONA-1", "Review as a senior engineer who wants every name to be clear.\n")
	return p
}

func TestAdaptersPublishOnlyActivePersonas(t *testing.T) {
	for _, c := range []struct{ adapter, skills string }{
		{"claude-code", ".claude/skills"},
		{"agents-md", ".agents/skills"},
	} {
		t.Run(c.adapter, func(t *testing.T) {
			p := reviewing(t)
			persona := c.skills + "/implement/personas/reviewer.md"

			p.MustRun("publish", c.adapter)
			if _, ok := tree(t, p)[persona]; ok {
				t.Errorf("a proposed Persona was published at %s", persona)
			}

			humanMove(t, p, "PERSONA-1", "active")
			p.MustRun("publish", c.adapter)
			if got := p.Read(persona); got != "Review as a senior engineer who wants every name to be clear.\n" {
				t.Errorf("published Persona = %q, want its description", got)
			}
			if _, body := skillFrontmatter(t, p.Read(c.skills+"/implement/SKILL.md")); !strings.Contains(body, "[reviewer](personas/reviewer.md)") {
				t.Errorf("SKILL.md body =\n%s\nwant a link to the reviewer Persona", body)
			}

			humanMove(t, p, "PERSONA-1", "retired")
			if r := p.MustRun("publish", c.adapter); !strings.Contains(r.Stdout, "removed "+persona) {
				t.Errorf("publishing after retiring = %q, want %s removed", r.Stdout, persona)
			}
		})
	}
}

func TestEveryActivePersonaIsPublishedForTheAgentToAdoptThoughNoSkillNamesIt(t *testing.T) {
	for _, c := range []struct{ adapter, guide, persona, link string }{
		{"claude-code", ".claude/skills/jigflow/SKILL.md", ".claude/skills/jigflow/personas/reviewer.md", "[reviewer](personas/reviewer.md)"},
		{"agents-md", "AGENTS.md", ".agents/personas/reviewer.md", "[reviewer](.agents/personas/reviewer.md)"},
	} {
		t.Run(c.adapter, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.MustRun("create", "Persona", "--title", "reviewer")
			describePersona(p, "PERSONA-1", "Review for clarity.\n")
			p.MustRun("create", "Persona", "--title", "architect")

			p.MustRun("publish", c.adapter)
			guide := p.Read(c.guide)
			if !strings.Contains(guide, "No Persona is active in this project yet.") || !strings.Contains(guide, "jfl create Persona --title <name>") {
				t.Errorf("%s =\n%s\nwant no Persona active, and how to propose one", c.guide, guide)
			}

			humanMove(t, p, "PERSONA-1", "active")
			p.MustRun("publish", c.adapter)
			if guide := p.Read(c.guide); !strings.Contains(guide, "Active in this project:\n\n- "+c.link+"\n") {
				t.Errorf("%s =\n%s\nwant only the reviewer listed, as %s", c.guide, guide, c.link)
			}
			if got := p.Read(c.persona); got != "Review for clarity.\n" {
				t.Errorf("%s = %q, want the Persona's description", c.persona, got)
			}
		})
	}
}

// library makes dir the user's configuration directory, holding the
// Persona Library, for every command the project runs from now on, and
// writes the given Personas into the Library.
func library(t *testing.T, p *clitest.Project, personas map[string]string) {
	t.Helper()
	config := t.TempDir()
	p.Setenv("XDG_CONFIG_HOME", config)
	for name, md := range personas {
		path := filepath.Join(config, "jigflow", "personas", name+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestThePersonaLibraryIsReusedAcrossProjects(t *testing.T) {
	personas := map[string]string{"reviewer": "Review for clarity.\n"}
	for _, name := range []string{"shop", "blog"} {
		p := ticketPlaybook(t)
		library(t, p, personas)
		p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\npersonas: [{name: reviewer, fallback: A reviewer.}]\n---\nWork on it.\n")
		p.MustRun("publish", "claude-code")
		if got := p.Read(".claude/skills/implement/personas/reviewer.md"); got != "Review for clarity.\n" {
			t.Errorf("%s: published reviewer = %q, want the Library's", name, got)
		}
	}
}

func TestAProjectPersonaOverridesTheLibrarysOfTheSameName(t *testing.T) {
	const published = ".claude/skills/jigflow/personas/reviewer.md"
	p := ticketPlaybook(t)
	library(t, p, map[string]string{"reviewer": "The Library's reviewer.\n", "tester": "The Library's tester.\n"})

	// A Persona the Playbook ships overrides the Library's.
	p.Write(".jigflow/personas/reviewer.md", "The Playbook's reviewer.\n")
	p.MustRun("publish", "claude-code")
	if got := p.Read(published); got != "The Playbook's reviewer.\n" {
		t.Errorf("reviewer = %q, want the Playbook's", got)
	}

	// A proposed Persona Artifact isn't usable yet, so it overrides nothing;
	// once active it overrides both.
	p.MustRun("create", "Persona", "--title", "reviewer")
	describePersona(p, "PERSONA-1", "The project's reviewer.\n")
	p.MustRun("publish", "claude-code")
	if got := p.Read(published); got != "The Playbook's reviewer.\n" {
		t.Errorf("reviewer = %q, want the Playbook's while the project's is proposed", got)
	}
	humanMove(t, p, "PERSONA-1", "active")
	p.MustRun("publish", "claude-code")
	if got := p.Read(published); got != "The project's reviewer.\n" {
		t.Errorf("reviewer = %q, want the active Persona Artifact's", got)
	}

	// Retiring a project Persona retires it in this project, rather than
	// bringing the Library's back.
	p.MustRun("create", "Persona", "--title", "tester")
	humanMove(t, p, "PERSONA-2", "retired")
	p.MustRun("publish", "claude-code")
	if _, ok := tree(t, p)[".claude/skills/jigflow/personas/tester.md"]; ok {
		t.Errorf("the Library's tester was published although the project retired its own")
	}
}

func TestASkillTellsTheAgentToProposeAMissingPersonaFromItsFallbackDescription(t *testing.T) {
	p := reviewing(t) // the reviewer Persona is only proposed
	p.MustRun("publish", "claude-code")

	_, body := skillFrontmatter(t, p.Read(".claude/skills/implement/SKILL.md"))
	for _, want := range []string{
		"The reviewer Persona isn't active in this project, so work without it.",
		"`jfl create Persona --title reviewer`",
		"> A senior engineer who reviews for clarity.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SKILL.md body =\n%s\nwant %q", body, want)
		}
	}
	if strings.Contains(body, "personas/reviewer.md") {
		t.Errorf("SKILL.md body =\n%s\nwant no link to a Persona that isn't active", body)
	}
}
