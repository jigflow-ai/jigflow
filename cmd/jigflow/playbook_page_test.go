package main_test

import (
	"net/http"
	"strings"
	"testing"
)

// specType is an Artifact Type the tests below declare in a Base Playbook,
// in the project, or in both.
const specType = "name: Spec\nprefix: S\nstatuses: [draft, done]\ninitial: [draft]\nfinal: [done]\ntransitions:\n  - {from: draft, to: done}\n"

func TestThePlaybookPageSaysWhereEachArtifactTypeComesFrom(t *testing.T) {
	p := extending(t)
	p.Write("base/types/spec.yaml", specType)
	p.Write(".jigflow/types/spec.yaml", specType)
	p.Write(".jigflow/types/note.yaml", strings.NewReplacer("Spec", "Note", "prefix: S", "prefix: N").Replace(specType))
	ui := p.StartUI()

	if backlog := get(t, ui, "/"); !strings.Contains(backlog, `<a href="/playbook"`) {
		t.Errorf("the navigation should link the Playbook page:\n%s", text(backlog))
	}
	types := section(t, get(t, ui, "/playbook"), "Artifact Types")
	wantText(t, types,
		"Ticket T files Base Playbook base",
		"Spec S files project, overriding Base Playbook base",
		"Note N files project",
		"Persona PERSONA files built into jfl")
}

func TestThePlaybookPageSaysWhatComesFromABuiltInBasePlaybook(t *testing.T) {
	p := larapilot(t)
	ui := p.StartUI()

	wantText(t, section(t, get(t, ui, "/playbook"), "Artifact Types"), "Task T files builtin larapilot")
}

func TestThePlaybookPageListsTheBindingsAndEachSkillsInvocationModeAndStatuses(t *testing.T) {
	p := extending(t)
	p.Write("base/skills/triage/SKILL.md", "---\ndescription: Sort new Tickets.\nchanges: false\ninvocation: user\n---\nSort them.\n")
	writeSkillIn(p, ".jigflow", "implement", true)
	ui := p.StartUI()
	page := get(t, ui, "/playbook")

	wantText(t, section(t, page, "Bindings"),
		"Ticket ready-for-agent /implement Base Playbook base",
		"Ticket in-progress /implement Base Playbook base")
	skills := section(t, page, "Skills")
	wantText(t, skills,
		"implement bound Ticket ready-for-agent, Ticket in-progress project, overriding Base Playbook base",
		"triage Sort new Tickets. user no Binding Base Playbook base")
}

func TestThePlaybookPageListsGuidelinesAndWhetherEachPersonaIsShippedProposedActiveOrRetired(t *testing.T) {
	p := extending(t)
	library(t, p, map[string]string{"editor": "Edit for brevity.\n", "tester": "Test the edges.\n"})
	p.Write("base/guidelines/go-style.md", "Run gofmt.\n")
	p.Write(".jigflow/guidelines/sql.md", "Name tables in the plural.\n")
	p.Write("base/personas/reviewer.md", "Review for clarity.\n")
	p.Write(".jigflow/personas/architect.md", "Keep modules deep.\n")
	for _, name := range []string{"security", "writer", "tester"} {
		p.MustRun("create", "Persona", "--title", name)
	}
	humanMove(t, p, "PERSONA-2", "active")
	humanMove(t, p, "PERSONA-3", "retired")
	ui := p.StartUI()
	page := get(t, ui, "/playbook")

	wantText(t, section(t, page, "Guidelines"), "go-style Base Playbook base", "sql project")
	personas := section(t, page, "Personas")
	wantText(t, personas,
		"architect shipped project",
		"editor shipped Persona Library",
		"reviewer shipped Base Playbook base",
		"security proposed Persona Artifact PERSONA-1",
		"tester retired Persona Artifact PERSONA-3 (overriding Persona Library)",
		"writer active Persona Artifact PERSONA-2")
	if !strings.Contains(personas, `href="/artifacts/PERSONA-1"`) {
		t.Errorf("a Persona Artifact should link to its page:\n%s", personas)
	}
}

func TestThePlaybookPageSaysWhetherTheProjectGivesAGateDeclaredByNameACommandYet(t *testing.T) {
	p := larapilot(t)
	p.Write(".jigflow/playbook.yaml", "name: shop\nextends: {builtin: larapilot}\ngates:\n  tests: go test ./...\n")
	ui := p.StartUI()

	wantText(t, section(t, get(t, ui, "/playbook"), "Gates"),
		"tests Task in-progress → in-review go test ./... given by the project's Playbook file builtin larapilot",
		"lint Task in-progress → in-review no command yet: give it one under gates in .jigflow/playbook.yaml builtin larapilot")
}

func TestThePlaybookPageShowsAGateDeclaredWithItsCommand(t *testing.T) {
	p := gatedPlaybook(t, "      - {name: tests, cmd: make test}\n", "")
	ui := p.StartUI()

	wantText(t, section(t, get(t, ui, "/playbook"), "Gates"),
		"tests Ticket in-progress → in-review make test declared with its Transition project")
}

func TestSkillsGuidelinesAndPersonasOpenToTheirFileRenderedAsMarkdownWithRawHTMLAsText(t *testing.T) {
	p := extending(t)
	p.Write("base/skills/implement/SKILL.md", "---\ndescription: Build the Ticket.\nchanges: true\ninvocation: bound\n---\n## Steps\n\n- [ ] Read the Ticket\n\n<script>alert(\"owned\")</script>\n")
	p.Write(".jigflow/guidelines/sql.md", "# SQL\n\n| Table | Name |\n| --- | --- |\n| users | plural |\n")
	p.Write("base/personas/reviewer.md", "Review for **clarity**.\n")
	library(t, p, map[string]string{"editor": "Edit for *brevity*.\n"})
	p.MustRun("create", "Persona", "--title", "writer")
	describePersona(p, "PERSONA-1", "Write `plainly`.\n")
	ui := p.StartUI()

	page := get(t, ui, "/playbook")
	for _, path := range []string{"/playbook/skills/implement", "/playbook/guidelines/sql", "/playbook/personas/reviewer", "/playbook/personas/editor", "/playbook/personas/writer"} {
		if !strings.Contains(page, `href="`+path+`"`) {
			t.Errorf("the Playbook page should link %s", path)
		}
	}

	skill := get(t, ui, "/playbook/skills/implement")
	wantText(t, skill, "Skill implement", "Build the Ticket.")
	wantText(t, section(t, skill, "About"),
		"Invocation Mode bound", "Changes code or Artifacts yes",
		"Bound to Ticket ready-for-agent, Ticket in-progress",
		"Comes from Base Playbook base", "File base/skills/implement/SKILL.md")
	contents := section(t, skill, "Contents")
	for _, want := range []string{"<h2", `type="checkbox"`} {
		if !strings.Contains(contents, want) {
			t.Errorf("the Skill should be rendered with %q:\n%s", want, contents)
		}
	}
	if strings.Contains(skill, `<script>alert("owned")</script>`) {
		t.Errorf("raw HTML in a Skill must not reach the page as HTML:\n%s", contents)
	}
	wantText(t, contents, `<script>alert("owned")</script>`)

	guideline := get(t, ui, "/playbook/guidelines/sql")
	wantText(t, section(t, guideline, "About"), "Comes from project", "File .jigflow/guidelines/sql.md")
	if contents := section(t, guideline, "Contents"); !strings.Contains(contents, "<td>users</td>") {
		t.Errorf("the Guideline should be rendered as Markdown:\n%s", contents)
	}

	reviewer := get(t, ui, "/playbook/personas/reviewer")
	wantText(t, section(t, reviewer, "About"), "State shipped", "Comes from Base Playbook base", "File base/personas/reviewer.md")
	if contents := section(t, reviewer, "Contents"); !strings.Contains(contents, "<strong>clarity</strong>") {
		t.Errorf("the Persona should be rendered as Markdown:\n%s", contents)
	}
	if contents := section(t, get(t, ui, "/playbook/personas/editor"), "Contents"); !strings.Contains(contents, "<em>brevity</em>") {
		t.Errorf("the Library's Persona should be rendered as Markdown:\n%s", contents)
	}
	writer := get(t, ui, "/playbook/personas/writer")
	wantText(t, section(t, writer, "About"), "State proposed", "Comes from Persona Artifact PERSONA-1")
	if contents := section(t, writer, "Contents"); !strings.Contains(contents, "<code>plainly</code>") {
		t.Errorf("the Persona Artifact's body should be rendered as Markdown:\n%s", contents)
	}
}

func TestAPartThePlaybookDoesNotHaveGetsAPageSayingSo(t *testing.T) {
	p := extending(t)
	ui := p.StartUI()

	for path, want := range map[string]string{
		"/playbook/skills/deploy":     "there is no Skill deploy in the Playbook",
		"/playbook/guidelines/sql":    "there is no Guideline sql in the Playbook",
		"/playbook/personas/reviewer": "there is no Persona reviewer in the project",
	} {
		page := ui.Get(path)
		if page.Status != 404 {
			t.Errorf("GET %s: status %d, want 404", path, page.Status)
		}
		wantText(t, page.HTML, want)
	}
}

func TestThePlaybookPageOffersNoWayToChangeThePlaybook(t *testing.T) {
	p := larapilot(t)
	ui := p.StartUI()

	for _, path := range []string{"/playbook", "/playbook/skills/implement", "/playbook/guidelines/conventions", "/playbook/personas/reviewer"} {
		if page := get(t, ui, path); strings.Contains(page, "<form") {
			t.Errorf("%s offers a form:\n%s", path, text(page))
		}
		if page := ui.Post(path, nil); page.Status != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status %d, want 405", path, page.Status)
		}
	}
}

func TestThePlaybookPagesAnswerOnlyThisMachineAndUpdateThemselves(t *testing.T) {
	p := larapilot(t)
	ui := p.StartUI()

	for _, path := range []string{"/playbook", "/playbook/skills/implement", "/playbook/guidelines/conventions", "/playbook/personas/reviewer", "/playbook/skills/deploy"} {
		req := ui.NewRequest("GET", path, nil)
		req.Host = "attacker.example:80"
		if page := ui.Do(req); page.Status != http.StatusForbidden {
			t.Errorf("GET %s for attacker.example: status %d, want 403", path, page.Status)
		}
		if page := ui.Get(path); !listens(page.HTML) || !strings.Contains(page.HTML, `var here = "`+path+`"`) {
			t.Errorf("%s (status %d) should reload itself when the project changes", path, page.Status)
		}
	}

	s := listenAsPage(t, ui, get(t, ui, "/playbook"))
	p.Write(".jigflow/guidelines/sql.md", "Name tables in the plural.\n")
	wantSignal(t, s, "a new Guideline")
	wantText(t, section(t, get(t, ui, "/playbook"), "Guidelines"), "sql project")
}
