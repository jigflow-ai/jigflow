package main_test

import (
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakelinear"
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

func TestAnAttributedCommentOnAGitHubIssueLeadsWithThePersona(t *testing.T) {
	p, gh := githubPlaybook(t)
	p.Write(".jigflow/personas/reviewer.md", "Review it.\n")
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket", "ready-for-agent"}})

	agentComments(t, p, "A", "T-41", "--persona", "reviewer", "Names are clear.")
	p.MustRun("comment", "T-41", "--persona", "reviewer", "Agreed.")
	p.MustRun("comment", "T-41", "Thanks.")

	want := []string{
		"**As reviewer:**\n\nNames are clear.\n\n_Written by an AI agent through JigFlow._",
		"**As reviewer:**\n\nAgreed.",
		"Thanks.",
	}
	if got := gh.Issue(t, 41).Comments; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("issue 41's comments = %q, want %q", got, want)
	}
	if show := p.MustRun("show", "T-41").Stdout; !strings.Contains(show, "## Comment 1\n\n**As reviewer:**\n\nNames are clear.") || !strings.Contains(show, "## Comment 2\n\n**As reviewer:**\n\nAgreed.") {
		t.Errorf("jfl show should print the attributed comments:\n%s", show)
	}
}

func TestAnAttributedCommentOnALinearIssueLeadsWithThePersona(t *testing.T) {
	p, ln := linearPlaybook(t)
	p.Write(".jigflow/personas/security-reviewer.md", "Review for security.\n")
	ln.Add(fakelinear.Issue{Title: "Add login page", Labels: []string{"ticket"}})

	agentComments(t, p, "A", "T-41", "--persona", "security-reviewer", "No secrets logged.")
	p.MustRun("comment", "T-41", "--persona", "security-reviewer", "Agreed.")

	want := []string{
		"**As security-reviewer:**\n\nNo secrets logged.\n\n_Written by an AI agent through JigFlow._",
		"**As security-reviewer:**\n\nAgreed.",
	}
	if got := ln.Issue(t, 41).Comments; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("issue ENG-41's comments = %q, want %q", got, want)
	}
}

func TestATrackerCommentIsRefusedAPersonaThatIsntUsableAndNothingIsWritten(t *testing.T) {
	p, gh := githubPlaybook(t)
	p.Write(".jigflow/personas/reviewer.md", "Review it.\n")
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket", "ready-for-agent"}})

	r := p.RunInSession("A", "comment", "T-41", "--persona", "nobody", "Looks fine.")
	if want := `no Persona "nobody" is usable in this project; the usable ones are reviewer`; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("a tracker comment as an unknown Persona: exit %d, stderr %q; want %q", r.ExitCode, r.Stderr, want)
	}
	if got := gh.Issue(t, 41).Comments; len(got) != 0 {
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

func TestTheArtifactPageShowsAPersonaChipOnAttributedTrackerComments(t *testing.T) {
	p := trackerPlaybook(t)
	p.Write(".jigflow/personas/security-reviewer.md", "Review for security.\n")
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
	}})
	agentComments(t, p, "A", "T-41", "--persona", "security-reviewer", "Checked `login.go`: **no** secrets logged.")
	p.MustRun("comment", "T-41", "Thanks.")
	ui := p.StartUI()

	comments := section(t, get(t, ui, "/artifacts/T-41"), "Comments")
	for _, want := range []string{
		`<strong>As</strong> <span class="pill persona" title="Persona">security-reviewer</span>`,
		"Checked <code>login.go</code>: <strong>no</strong> secrets logged.",
		"<em>Written by an AI agent through JigFlow.</em>",
		"<p>Thanks.</p>",
	} {
		if !strings.Contains(comments, want) {
			t.Errorf("the comments should show %q:\n%s", want, comments)
		}
	}
	if n := strings.Count(comments, `class="pill persona"`); n != 1 {
		t.Errorf("the comments show %d Persona chips, want one for the attributed comment:\n%s", n, comments)
	}
	if strings.Contains(comments, "**As") {
		t.Errorf("the lead line should be shown as a chip, not as its Markdown:\n%s", comments)
	}
}
