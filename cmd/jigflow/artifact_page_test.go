package main_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestEachArtifactHasAPageTheBacklogLinksTo(t *testing.T) {
	p := larapilot(t)
	p.MustRun("create", "PRD", "--title", "Shop checkout", "--status", "adopt")
	p.MustRun("create", "Requirement", "--title", "Pay by card", "--field", "priority=must", "--link", "part_of=PRD-1")
	ui := p.StartUI()

	if backlog := get(t, ui, "/"); !strings.Contains(backlog, `<a href="/artifacts/REQ-1">REQ-1</a>`) {
		t.Errorf("the backlog should link REQ-1 to its page:\n%s", text(backlog))
	}
	page := get(t, ui, "/artifacts/REQ-1")
	wantText(t, page, "REQ-1 Pay by card")
	wantText(t, section(t, page, "About"), "Requirement", "specified", "priority must")
}

func TestAnArtifactsPageRendersItsBodyAndShowsRawHTMLAsText(t *testing.T) {
	p := larapilot(t)
	p.MustRun("create", "PRD", "--title", "Shop checkout", "--status", "adopt")
	p.Write(".jigflow/state/PRD-1.md", p.Read(".jigflow/state/PRD-1.md")+`
## Journeys

- [x] A shopper pays by card
- [ ] A shopper pays by gift card

| Target | Value |
| --- | --- |
| p95 checkout | 800 ms |

<script>alert("owned")</script>

A shopper <img src=x onerror="alert('owned')"> pays.
`)
	ui := p.StartUI()

	page := get(t, ui, "/artifacts/PRD-1")
	body := section(t, page, "Body")
	for _, want := range []string{"<h2", "Journeys", `<input checked="" disabled="" type="checkbox"`, "<table>", "<td>p95 checkout</td>"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body should be rendered with %q:\n%s", want, body)
		}
	}
	if strings.Contains(page, `<script>alert("owned")</script>`) {
		t.Errorf("raw HTML in a body must not reach the page as HTML:\n%s", body)
	}
	wantText(t, body, `<script>alert("owned")</script>`, `A shopper <img src=x onerror="alert('owned')"> pays.`)
	if strings.Contains(page, "<img") {
		t.Errorf("inline HTML in a body must not reach the page as HTML:\n%s", body)
	}
}

func TestAnArtifactsPageFollowsItsLinksBothWays(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.MustRun("create", "Ticket", "--title", "Reset-token table", "--link", "part_of=S-1")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "part_of=S-1", "--link", "blocked_by=T-1")
	p.MustRun("create", "Ticket", "--title", "Reset email", "--link", "part_of=S-1")
	p.Write("tests-pass", "")
	p.Write("record.sh", "")
	for _, to := range []string{"in-progress", "in-review", "ready-to-merge"} {
		p.MustRun("move", "T-3", to)
	}
	humanMove(t, p, "T-3", "done")
	ui := p.StartUI()

	ticket := get(t, ui, "/artifacts/T-2")
	links := section(t, ticket, "Links")
	wantText(t, links, "blocked_by T-1 Reset-token table ready-for-agent", "part_of S-1 Password reset by email ready-for-agent")
	for _, id := range []string{"T-1", "S-1"} {
		if !strings.Contains(links, `href="/artifacts/`+id+`"`) {
			t.Errorf("T-2's Links should link %s to its page:\n%s", id, links)
		}
	}

	spec := get(t, ui, "/artifacts/S-1")
	wantText(t, section(t, spec, "Linked here"),
		"Ticket part_of 1 of 3 finished",
		"T-1 Reset-token table ready-for-agent", "T-2 Reset endpoint ready-for-agent", "T-3 Reset email done")
	wantText(t, section(t, get(t, ui, "/artifacts/T-3"), "Linked here"), "Nothing links here.")
}

func TestAnArtifactsPageShowsItsClaimWhoItWaitsOnAndItsLedgerHistory(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p) // Focus: T-1
	p.At("2026-09-28T09:10:00Z")
	agentMove(t, p, "T-1", "in-progress", 0)
	p.At("2026-09-28T09:40:00Z")
	agentMove(t, p, "T-1", "in-review", 0) // no Binding: out of Focus
	p.MustRun("create", "Ticket", "--title", "Signup page")
	p.At("2026-09-28T09:55:00Z")
	agentMove(t, p, "T-2", "in-progress", 0)
	p.At("2026-09-28T10:15:00Z")
	ui := p.StartUI()

	t1 := get(t, ui, "/artifacts/T-1")
	wantText(t, section(t, t1, "About"), "Status in-review", "Work human work", "Claim none")
	wantText(t, section(t, t1, "History"),
		"created in ready-for-agent 10m",
		"ready-for-agent → in-progress 30m",
		"in-progress → in-review 35m so far",
		"Agent time 40m")
	wantText(t, section(t, get(t, ui, "/artifacts/T-2"), "About"), "Work agent work", "Claim agent session A")
}

func TestAnArtifactsPageSaysWhyNextSkipsIt(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "blocked_by=T-1")
	ui := p.StartUI()

	wantText(t, section(t, get(t, ui, "/artifacts/T-2"), "About"), "Work agent work", "not ready")
}

func TestAnArtifactsPageOffersItsHumanTransitionsAndThePendingProposalsTouchingIt(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose: %s", r.Stderr)
	}
	p.Write("other.yaml", "summary: file a crash\nitems:\n  - {create: Issue, title: Crash on logout}\n")
	p.MustRun("propose", "other.yaml")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.Write("tests-pass", "")
	for _, to := range []string{"in-progress", "in-review", "ready-to-merge"} {
		p.MustRun("move", "T-1", to)
	}
	ui := p.StartUI()

	proposals := section(t, get(t, ui, "/artifacts/S-1"), "Pending Proposals")
	wantText(t, proposals, "P-1 break S-1 into 3 tickets from agent session A")
	if !strings.Contains(proposals, `href="/#P-1"`) || strings.Contains(text(proposals), "P-2") {
		t.Errorf("S-1's page should link P-1, which touches it, and not P-2:\n%s", proposals)
	}
	wantText(t, section(t, get(t, ui, "/artifacts/T-1"), "Pending Proposals"), "No pending Proposal touches it.")

	moves := section(t, get(t, ui, "/artifacts/T-1"), "Human Transitions")
	if !strings.Contains(moves, `action="/artifacts/T-1/move"`) || !strings.Contains(text(moves), "→ done") {
		t.Errorf("T-1's page should offer its Human Transition to done:\n%s", moves)
	}
	looking := section(t, get(t, p.StartUIInSession("A"), "/artifacts/T-1"), "Human Transitions")
	if strings.Contains(looking, "<form") {
		t.Errorf("a Dashboard an agent session started must offer no Human Transition:\n%s", looking)
	}
	wantText(t, looking, "agent session A started this Dashboard")
}

func TestPendingProposalsLinkTheArtifactsTheyName(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose: %s", r.Stderr)
	}
	ui := p.StartUI()

	proposals := section(t, get(t, ui, "/"), "Pending Proposals")
	if !strings.Contains(proposals, `href="/artifacts/S-1"`) {
		t.Errorf("P-1 should link S-1, which it names, to its page:\n%s", proposals)
	}
	if strings.Contains(proposals, `href="/artifacts/token-table"`) {
		t.Errorf("a ref isn't an Artifact yet, and has no page:\n%s", proposals)
	}
}

func TestTheArtifactPageOfATrackerItemShowsItsBodyAndCommentsAndTellsTrackerProblemsApart(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}, "body": "## Acceptance\n\n- [ ] a form", "comments": []string{"Use the design system's inputs."}},
	}})
	ui := p.StartUI()

	page := get(t, ui, "/artifacts/T-41")
	wantText(t, page, "T-41 Add login page")
	wantText(t, section(t, page, "Body"), "Acceptance", "a form")
	wantText(t, section(t, page, "Comments"), "Use the design system's inputs.")

	setTracker(t, p, map[string]any{"fail": "network"})
	failed := ui.Get("/artifacts/T-41")
	if failed.Status != http.StatusBadGateway {
		t.Errorf("GET /artifacts/T-41 with the tracker unreachable: status %d, want 502", failed.Status)
	}
	wantText(t, failed.HTML, "tracker problem, not a workflow refusal")
}

func TestAnUnknownArtifactsPageSaysThereIsNone(t *testing.T) {
	p := pocockPlaybook(t)
	ui := p.StartUI()

	page := ui.Get("/artifacts/S-9")
	if page.Status != http.StatusNotFound {
		t.Errorf("GET /artifacts/S-9: status %d, want 404", page.Status)
	}
	wantText(t, page.HTML, "there is no Artifact S-9")
}

func TestAnArtifactsPageAnswersOnlyThisMachine(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	ui := p.StartUI()

	req := ui.NewRequest("GET", "/artifacts/S-1", nil)
	req.Host = "attacker.example:80"
	if page := ui.Do(req); page.Status != http.StatusForbidden {
		t.Errorf("a request for attacker.example got status %d, want 403", page.Status)
	}
	wantText(t, section(t, get(t, ui, "/artifacts/S-1"), "Body"), "No body yet.")
}
