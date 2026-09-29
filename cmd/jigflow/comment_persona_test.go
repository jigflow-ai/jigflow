package main_test

import (
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

func TestAnAgentSessionAttributesACommentToAPersona(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/personas/security-reviewer.md", "Review for security.\n")
	p.MustRun("create", "Ticket", "--title", "Add login page")

	r := p.RunInSession("A", "comment", "T-1", "--persona", "security-reviewer", "No secrets logged; checked the auth handlers.")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "commented on T-1") {
		t.Fatalf("an agent attributing a comment: exit %d, stdout %q, stderr %q", r.ExitCode, r.Stdout, r.Stderr)
	}
	want := "**Comment by agent session A as security-reviewer:**\n\nNo secrets logged; checked the auth handlers.\n"
	if got := p.Read(".jigflow/state/T-1.md"); !strings.HasSuffix(got, want) {
		t.Errorf("the comment's heading should name the Persona after the agent session:\n%s", got)
	}
	if show := p.MustRun("show", "T-1").Stdout; !strings.Contains(show, want) {
		t.Errorf("jfl show should print the attributed comment:\n%s", show)
	}
}

func TestACommentIsRefusedAPersonaThatIsntUsableAndNothingIsWritten(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/personas/tester.md", "Test it.\n")
	p.MustRun("create", "Ticket", "--title", "Add login page")
	p.MustRun("create", "Persona", "--title", "architect")
	p.MustRun("create", "Persona", "--title", "reviewer")
	humanMove(t, p, "PERSONA-2", "active")
	p.MustRun("create", "Persona", "--title", "auditor")
	humanMove(t, p, "PERSONA-3", "retired")
	before := p.Read(".jigflow/state/T-1.md")

	for name, why := range map[string]string{"nobody": "unknown", "architect": "proposed", "auditor": "retired"} {
		r := p.RunInSession("A", "comment", "T-1", "--persona", name, "Looks fine.")
		want := `no Persona "` + name + `" is usable in this project; the usable ones are reviewer, tester`
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("a comment as the %s Persona %s: exit %d, stderr %q; want %q", why, name, r.ExitCode, r.Stderr, want)
		}
	}
	if got := p.Read(".jigflow/state/T-1.md"); got != before {
		t.Errorf("a refused comment changed T-1:\n%s", got)
	}
}

func TestAPersonAttributesACommentToAnActivePersonaOrOneOfTheLibrary(t *testing.T) {
	p := ticketPlaybook(t)
	library(t, p, map[string]string{"tester": "The Library's tester.\n"})
	p.MustRun("create", "Ticket", "--title", "Add login page")
	p.MustRun("create", "Persona", "--title", "reviewer")
	humanMove(t, p, "PERSONA-1", "active")

	p.MustRun("comment", "T-1", "--persona", "reviewer", "Names are clear.")
	p.MustRun("comment", "T-1", "--persona", "tester", "Edge cases covered.")
	p.MustRun("comment", "T-1", "Thanks.")

	want := "**Comment as reviewer:**\n\nNames are clear.\n\n**Comment as tester:**\n\nEdge cases covered.\n\n**Comment:**\n\nThanks.\n"
	if got := p.Read(".jigflow/state/T-1.md"); !strings.HasSuffix(got, want) {
		t.Errorf("a person's comments should be headed as theirs, naming the Persona when they give one:\n%s", got)
	}
	if show := p.MustRun("show", "T-1").Stdout; !strings.Contains(show, want) {
		t.Errorf("jfl show should print the attributed comments:\n%s", show)
	}
}

func TestAProjectPersonaOverridesTheLibrarysForAComment(t *testing.T) {
	p := ticketPlaybook(t)
	library(t, p, map[string]string{"auditor": "The Library's auditor.\n", "tester": "The Library's tester.\n"})
	p.MustRun("create", "Ticket", "--title", "Add login page")
	p.MustRun("create", "Persona", "--title", "auditor")
	humanMove(t, p, "PERSONA-1", "retired")
	before := p.Read(".jigflow/state/T-1.md")

	r := p.RunInSession("A", "comment", "T-1", "--persona", "auditor", "Nothing to audit.")
	if want := `no Persona "auditor" is usable in this project; the usable ones are tester`; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("a comment as a Persona the project retired over the Library's: exit %d, stderr %q; want %q", r.ExitCode, r.Stderr, want)
	}
	if got := p.Read(".jigflow/state/T-1.md"); got != before {
		t.Errorf("a refused comment changed T-1:\n%s", got)
	}
}

func TestACommentOnATrackerArtifactCantBeAttributedToAPersonaYet(t *testing.T) {
	p := trackerPlaybook(t)
	p.Write(".jigflow/personas/reviewer.md", "Review it.\n")
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
	}})

	r := p.RunInSession("A", "comment", "T-41", "--persona", "reviewer", "Names are clear.")
	if want := "only a comment on an Artifact kept in a file can be attributed to a Persona yet"; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("attributing a tracker comment: exit %d, stderr %q; want %q", r.ExitCode, r.Stderr, want)
	}
	if got := item(t, p, "41").Comments; len(got) != 0 {
		t.Errorf("a refused comment was added in the tracker: %q", got)
	}
}

func TestTheArtifactPageShowsAPersonaChipOnAttributedComments(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/personas/security-reviewer.md", "Review for security.\n")
	p.MustRun("create", "Ticket", "--title", "Add login page")
	agentComments(t, p, "A", "T-1", "--persona", "security-reviewer", "Checked `login.go`: **no** secrets logged.")
	p.MustRun("comment", "T-1", "--persona", "security-reviewer", "Agreed.")
	agentComments(t, p, "A", "T-1", "Done for now.")
	p.MustRun("comment", "T-1", "Thanks.")
	ui := p.StartUI()

	page := get(t, ui, "/artifacts/T-1")
	body := section(t, page, "Body")
	for _, want := range []string{
		`<strong>Comment by agent session A as</strong> <span class="pill persona" title="Persona">security-reviewer</span>`,
		`<strong>Comment as</strong> <span class="pill persona" title="Persona">security-reviewer</span>`,
		"Checked <code>login.go</code>: <strong>no</strong> secrets logged.",
		"<p><strong>Comment by agent session A:</strong></p>",
		"<p><strong>Comment:</strong></p>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body should show %q:\n%s", want, body)
		}
	}
	if n := strings.Count(body, `class="pill persona"`); n != 2 {
		t.Errorf("the body shows %d Persona chips, want one for each attributed comment:\n%s", n, body)
	}
	if form := section(t, page, "Comment"); strings.Contains(form, "persona") || strings.Contains(form, "Persona") {
		t.Errorf("the comment form should offer no Persona:\n%s", form)
	}
}

// agentComments runs jfl comment with args in the agent session session,
// failing the test unless it succeeds.
func agentComments(t *testing.T, p *clitest.Project, session string, args ...string) {
	t.Helper()
	if r := p.RunInSession(session, append([]string{"comment"}, args...)...); r.ExitCode != 0 {
		t.Fatalf("jfl comment %q in agent session %s: exit %d, stderr %q", args, session, r.ExitCode, r.Stderr)
	}
}
