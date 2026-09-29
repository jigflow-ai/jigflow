package main_test

import (
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

var (
	unread = regexp.MustCompile(`(?s)<style.*?</style>|<script.*?</script>`)
	tags   = regexp.MustCompile(`(?s)<[^>]*>`)
	spaces = regexp.MustCompile(`\s+`)
)

// text is what a person reads of an HTML fragment: its text, without the
// markup, styles and scripts, with runs of white space collapsed to one space.
func text(fragment string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(html.UnescapeString(tags.ReplaceAllString(unread.ReplaceAllString(fragment, " "), " ")), " "))
}

// section returns the HTML of the page's <section> headed by an <h2> or
// <h3> whose text is heading, failing the test when there is none.
func section(t *testing.T, page, heading string) string {
	t.Helper()
	for _, s := range strings.Split(page, "<section")[1:] {
		s, _, _ = strings.Cut(s, "</section>")
		for _, h := range []string{"h2", "h3"} {
			if _, rest, ok := strings.Cut(s, "<"+h); ok {
				if head, _, ok := strings.Cut(rest, "</"+h+">"); ok {
					if _, head, ok = strings.Cut(head, ">"); ok && text(head) == heading {
						return s
					}
				}
			}
		}
	}
	t.Fatalf("the page has no section headed %q:\n%s", heading, text(page))
	return ""
}

// wantText fails the test unless what a person reads of fragment contains
// each of want.
func wantText(t *testing.T, fragment string, want ...string) {
	t.Helper()
	got := text(fragment)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the page should read %q, but reads:\n%s", w, got)
		}
	}
}

// get fetches a page of the Dashboard and fails the test unless it is served.
func get(t *testing.T, ui *clitest.UI, path string) string {
	t.Helper()
	page := ui.Get(path)
	if page.Status != 200 {
		t.Fatalf("GET %s: status %d\n%s", path, page.Status, text(page.HTML))
	}
	return page.HTML
}

func TestTheDashboardShowsTheArtifactsOfEachTypeWithTheirStatus(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.MustRun("create", "Issue", "--title", "Crash on logout")
	ui := p.StartUI()

	page := get(t, ui, "/")
	wantText(t, section(t, page, "Spec"), "S-1", "Password reset by email", "ready-for-agent")
	wantText(t, section(t, page, "Issue"), "I-1", "Crash on logout", "needs-triage")
}

// row returns what a person reads of the table row of the Artifact id.
func row(t *testing.T, page, id string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, `<tr id="`+id+`"`)
	if !ok {
		t.Fatalf("the page has no row for %s:\n%s", id, text(page))
	}
	r, _, _ := strings.Cut(rest, "</tr>")
	return text("<tr" + r)
}

func TestTheDashboardShowsEachArtifactsClaimLinksAndWhoItWaitsOn(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.MustRun("create", "Ticket", "--title", "Reset-token table", "--status", "ready-for-agent", "--link", "part_of=S-1")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "part_of=S-1", "--link", "blocked_by=T-1")
	p.MustRun("create", "Ticket", "--title", "Reset email")
	agentMove(t, p, "T-1", "in-progress", 0)
	p.Write("tests-pass", "")
	for _, to := range []string{"in-progress", "in-review", "ready-to-merge"} {
		p.MustRun("move", "T-3", to)
	}
	p.MustRun("create", "Issue", "--title", "Crash on logout", "--status", "ready-for-agent")
	p.MustRun("move", "I-1", "done")
	ui := p.StartUI()

	page := get(t, ui, "/")
	for id, want := range map[string][]string{
		"T-1": {"Reset-token table", "in-progress", "agent session A", "part_of S-1", "agent work"},
		"T-2": {"Reset endpoint", "ready-for-agent", "blocked_by T-1", "part_of S-1", "agent work", "not ready"},
		"T-3": {"Reset email", "ready-to-merge", "human work"},
		"I-1": {"Crash on logout", "done", "finished"},
	} {
		got := row(t, page, id)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s's row should read %q, but reads %q", id, w, got)
			}
		}
	}
}

func TestTheDashboardPutsTheHumanQueueAndPendingProposalsFirst(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose: %s", r.Stderr)
	}
	p.Write("other.yaml", "summary: file a crash\nitems:\n  - {create: Issue, title: Crash on logout}\n")
	p.MustRun("propose", "other.yaml")
	p.MustRun("reject", "P-2")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.MustRun("create", "Ticket", "--title", "Signup page")
	p.Write("tests-pass", "")
	for _, to := range []string{"in-progress", "in-review", "ready-to-merge"} {
		p.MustRun("move", "T-1", to)
	}
	ui := p.StartUI()

	page := get(t, ui, "/")
	queue := section(t, page, "Waiting for you")
	wantText(t, queue, "T-1", "Login page", "ready-to-merge")
	if strings.Contains(text(queue), "T-2") {
		t.Errorf("T-2 is agent work, but the human queue lists it:\n%s", text(queue))
	}
	proposals := section(t, page, "Pending Proposals")
	wantText(t, proposals, "P-1", "break S-1 into 3 tickets", "agent session A",
		`create Ticket "Reset-token table"`, "move S-1 → ticketed")
	if strings.Contains(text(proposals), "P-2") {
		t.Errorf("P-2 was rejected, but is listed as pending:\n%s", text(proposals))
	}
	first := strings.Index(page, section(t, page, "Spec"))
	for _, s := range []string{queue, proposals} {
		if strings.Index(page, s) > first {
			t.Errorf("the human queue and pending Proposals should come before the backlog")
		}
	}
}

func TestTheDashboardSaysWhenNothingWaitsForAPerson(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")
	ui := p.StartUI()

	page := get(t, ui, "/")
	wantText(t, section(t, page, "Waiting for you"), "Nothing waits for you")
	wantText(t, section(t, page, "Pending Proposals"), "No pending Proposals")
}

func TestTheDashboardShowsTheLedgerSummedPerArtifactAndPerStatus(t *testing.T) {
	p := ticketPlaybook(t)
	config := claudeCode(t, p)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p) // Focus: T-1
	p.At("2026-09-28T09:10:00Z")
	agentMove(t, p, "T-1", "in-progress", 0)
	p.At("2026-09-28T09:40:00Z")
	agentMove(t, p, "T-1", "in-review", 0) // no Binding: out of Focus
	p.MustRun("create", "Ticket", "--title", "Signup page")
	p.At("2026-09-28T09:55:00Z")
	agentNext(t, p) // Focus: T-2, after 15m unattributed
	mustHook(t, p, "Stop", "A", transcript(t, config, "A",
		assistant{at: "2026-09-28T09:20:00.000Z", id: "msg_1", input: 100, output: 20, cacheRead: 3000, cacheWrite: 400}))
	p.At("2026-09-28T10:15:00Z")
	ui := p.StartUI()

	page := get(t, ui, "/ledger")
	perArtifact := section(t, page, "Time per Artifact")
	wantText(t, perArtifact, "T-1", "Login page", "ready-for-agent 10m", "in-progress 30m", "in-review 35m so far", "40m")
	wantText(t, perArtifact, "100 input", "20 output", "3000 cache read", "400 cache write")
	wantText(t, section(t, page, "Time per Status"), "Ticket", "in-progress 30m over 1 Artifact", "in-review 35m over 1 Artifact, 1 there now")
	wantText(t, section(t, page, "Unattributed"), "Agent time 15m")
}

func TestTheDashboardSaysWhenTheLedgerIsEmpty(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()

	wantText(t, get(t, ui, "/ledger"), "The Ledger is empty")
}

// svg returns the first inline SVG drawing in fragment, failing the test
// when there is none.
func svg(t *testing.T, fragment string) string {
	t.Helper()
	_, rest, ok := strings.Cut(fragment, "<svg")
	if !ok {
		t.Fatalf("no drawing in:\n%s", text(fragment))
	}
	d, _, _ := strings.Cut(rest, "</svg>")
	return "<svg" + d + "</svg>"
}

func TestTheDashboardDrawsEachArtifactTypesStatusMachine(t *testing.T) {
	p := pocockPlaybook(t)
	ui := p.StartUI()

	page := get(t, ui, "/workflows")
	ticket := section(t, page, "Ticket")
	drawing := svg(t, ticket)
	// A box per Status, with the Skill it is bound to, and an arrow per
	// Transition, the Human Transition marked as such.
	wantText(t, drawing,
		"ready-for-agent /implement", "in-progress /implement", "in-review /code-review", "ready-to-merge", "done",
		"in-review → in-progress",
		"ready-to-merge → done: Human Transition",
	)
	if strings.Contains(text(drawing), "needs-triage") {
		t.Error("the Ticket drawing shows an Issue Status")
	}
	// Beside the drawing, what controls each move.
	wantText(t, ticket,
		"in-progress → in-review Gates: tests (test -f tests-pass)",
		"ready-to-merge → done Human Transition; Actions: commit",
	)
	wantText(t, svg(t, section(t, page, "Issue")), "needs-triage /triage", "ready-for-human")
}

func TestTheDashboardServesOnlyOnThisMachine(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()
	if !strings.HasPrefix(ui.URL, "http://127.0.0.1:") {
		t.Errorf("the Dashboard serves at %s, want a loopback address", ui.URL)
	}

	r := p.Run("ui", "--addr", "0.0.0.0:0")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "only on this machine") {
		t.Errorf("jfl ui on every interface exited %d, want 1 and a refusal\nstderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestTheDashboardRefusesRequestsAddressedToAnotherHost(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()

	// A web page elsewhere that points its own name at 127.0.0.1 reaches
	// the Dashboard under that name, which it refuses.
	req, err := http.NewRequest(http.MethodGet, ui.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "attacker.example:80"
	if page := ui.Do(req); page.Status != http.StatusForbidden {
		t.Errorf("a request for attacker.example got status %d, want 403", page.Status)
	}
	req.Host = strings.Replace(strings.TrimPrefix(strings.TrimSuffix(ui.URL, "/"), "http://"), "127.0.0.1", "localhost", 1)
	if page := ui.Do(req); page.Status != http.StatusOK {
		t.Errorf("a request for %s got status %d, want 200", req.Host, page.Status)
	}
}

func TestTheDashboardShowsArtifactsKeptInATrackerAndTellsTrackerProblemsApart(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
	}})
	ui := p.StartUI()
	if got := row(t, get(t, ui, "/"), "T-41"); !strings.Contains(got, "Add login page ready-for-agent") {
		t.Errorf("T-41's row reads %q, want its title and Status from the tracker", got)
	}

	setTracker(t, p, map[string]any{"fail": "network"})
	page := ui.Get("/")
	if page.Status != http.StatusBadGateway {
		t.Errorf("GET / with the tracker unreachable: status %d, want 502", page.Status)
	}
	wantText(t, page.HTML, "tracker problem, not a workflow refusal", `Connector "tracker" could not reach the tracker`)
	// The shape of the workflow doesn't need the tracker.
	get(t, ui, "/workflows")
}
