package main_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// timelineRow returns the HTML of the Timeline's row of the Artifact id,
// with the rows nested under it, failing the test when there is none.
func timelineRow(t *testing.T, page, id string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, `<li id="row-`+id+`"`)
	if !ok {
		t.Fatalf("the Timeline has no row for %s:\n%s", id, text(page))
	}
	depth := 1
	for i := 0; i < len(rest); i++ {
		switch {
		case strings.HasPrefix(rest[i:], "<li"):
			depth++
		case strings.HasPrefix(rest[i:], "</li>"):
			depth--
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	return rest
}

// ownBar returns the HTML of the row of the Artifact id without the rows
// nested under it.
func ownBar(t *testing.T, page, id string) string {
	t.Helper()
	row, _, _ := strings.Cut(timelineRow(t, page, id), "<ul")
	return row
}

func TestTheTimelineDrawsEachArtifactAsABarSplitByStatusFromTheLedger(t *testing.T) {
	p := ticketPlaybook(t)
	p.Setenv("TZ", "UTC")
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.At("2026-09-28T09:10:00Z")
	p.MustRun("move", "T-1", "in-progress")
	p.At("2026-09-28T09:40:00Z")
	p.MustRun("move", "T-1", "in-review")
	p.At("2026-09-28T10:15:00Z")
	ui := p.StartUI()

	if nav := get(t, ui, "/"); !strings.Contains(nav, `<a href="/timeline">Timeline</a>`) {
		t.Errorf("the navigation should link the Timeline:\n%s", nav)
	}
	page := get(t, ui, "/timeline")
	bar := ownBar(t, page, "T-1")
	wantText(t, bar, "T-1 Login page",
		"T-1 ready-for-agent from 28 Sep 2026 09:00 to 28 Sep 2026 09:10 (10m)",
		"T-1 in-progress from 28 Sep 2026 09:10 to 28 Sep 2026 09:40 (30m)")
	if !strings.Contains(bar, `href="/artifacts/T-1"`) {
		t.Errorf("T-1's bar should link to its page:\n%s", bar)
	}
}

func TestTheTimelineDrawsAnArtifactStillInAStatusUpToNowAndHatchesAPersonsQueue(t *testing.T) {
	p := ticketPlaybook(t)
	p.Setenv("TZ", "UTC")
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.At("2026-09-28T09:10:00Z")
	p.MustRun("move", "T-1", "in-progress")
	p.At("2026-09-28T09:40:00Z")
	p.MustRun("move", "T-1", "in-review") // no Binding: a person's queue
	p.At("2026-09-28T10:15:00Z")
	ui := p.StartUI()

	page := get(t, ui, "/timeline")
	wantText(t, page, "to 28 Sep 2026 10:15")
	bar := ownBar(t, page, "T-1")
	wantText(t, bar, "T-1 in-review, waiting on a person, from 28 Sep 2026 09:40 to now (35m so far)")
	rects := strings.Split(bar, "<rect")[1:]
	if len(rects) == 0 {
		t.Fatalf("T-1 has no bar:\n%s", bar)
	}
	for _, r := range rects {
		hatched := strings.Contains(r, "hatched")
		if queue := strings.Contains(r, "in-review"); hatched != queue {
			t.Errorf("only time in a Status with no Binding should be hatched, got hatched=%v for:\n%s", hatched, r)
		}
	}
	last := rects[len(rects)-1]
	var x, w float64
	if _, err := fmt.Sscanf(svgAttr(last, "x"), "%f%%", &x); err != nil {
		t.Fatalf("x of %s: %v", last, err)
	}
	if _, err := fmt.Sscanf(svgAttr(last, "width"), "%f%%", &w); err != nil {
		t.Fatalf("width of %s: %v", last, err)
	}
	if x+w < 99.9 {
		t.Errorf("T-1, still in-review, should be drawn up to now, the Timeline's right edge; it ends at %.1f%%", x+w)
	}
}

// svgAttr returns the value of the attribute name of the first tag in s.
func svgAttr(s, name string) string {
	_, v, _ := strings.Cut(s, " "+name+`="`)
	v, _, _ = strings.Cut(v, `"`)
	return v
}

func TestTheTimelineNestsEachArtifactUnderItsFirstDeclaredLinkToAnotherType(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: nesting\n")
	for name, prefix := range map[string]string{"1-spec": "Spec S", "2-epic": "Epic E"} {
		typ, pre, _ := strings.Cut(prefix, " ")
		p.Write(".jigflow/types/"+name+".yaml", "name: "+typ+"\nprefix: "+pre+"\nstatuses: [open, closed]\ninitial: [open]\nfinal: [closed]\ntransitions:\n  - {from: open, to: closed}\n")
	}
	// Declared part_of first: epic, before it by name, doesn't nest.
	p.Write(".jigflow/types/3-ticket.yaml", `name: Ticket
prefix: T
links:
  part_of: Spec
  blocked_by: Ticket
  epic: Epic
statuses: [open, closed]
initial: [open]
final: [closed]
transitions:
  - {from: open, to: closed}
`)
	p.MustRun("create", "Spec", "--title", "Password reset")
	p.MustRun("create", "Epic", "--title", "Accounts")
	p.MustRun("create", "Ticket", "--title", "Reset-token table", "--link", "part_of=S-1", "--link", "epic=E-1")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "part_of=S-1", "--link", "blocked_by=T-1")
	p.MustRun("create", "Ticket", "--title", "Audit log", "--link", "epic=E-1")
	ui := p.StartUI()

	page := get(t, ui, "/timeline")
	spec := timelineRow(t, page, "S-1")
	for _, id := range []string{"T-1", "T-2"} {
		if !strings.Contains(spec, `id="row-`+id+`"`) {
			t.Errorf("%s, part_of S-1, should be nested under S-1:\n%s", id, text(spec))
		}
	}
	if strings.Contains(timelineRow(t, page, "T-1"), `id="row-T-2"`) {
		t.Errorf("T-2 is blocked_by T-1, a Ticket like itself, and should not nest under it")
	}
	epic := timelineRow(t, page, "E-1")
	if strings.Contains(epic, `id="row-T-1"`) {
		t.Errorf("T-1's first declared Link to another Type is part_of, so it nests under S-1, not E-1:\n%s", text(epic))
	}
	if !strings.Contains(epic, `id="row-T-3"`) {
		t.Errorf("T-3 has only epic, and should nest under E-1:\n%s", text(epic))
	}
}

func TestTheTimelineDrawsTheAgentSessionTimeChargedToAnArtifactUnderItsBar(t *testing.T) {
	p := ticketPlaybook(t)
	p.Setenv("TZ", "UTC")
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p) // Focus: T-1
	p.At("2026-09-28T09:10:00Z")
	agentMove(t, p, "T-1", "in-progress", 0)
	p.At("2026-09-28T09:40:00Z")
	agentMove(t, p, "T-1", "in-review", 0) // no Binding: out of Focus
	p.MustRun("create", "Ticket", "--title", "Signup page")
	p.At("2026-09-28T10:15:00Z")
	ui := p.StartUI()

	page := get(t, ui, "/timeline")
	band := strings.Split(ownBar(t, page, "T-1"), `class="agent"`)
	if len(band) != 2 {
		t.Fatalf("T-1 should have one stretch of agent session time under its bar, has %d:\n%s", len(band)-1, ownBar(t, page, "T-1"))
	}
	wantText(t, band[1], "T-1 agent session A from 28 Sep 2026 09:00 to 28 Sep 2026 09:40 (40m)")
	if strings.Contains(ownBar(t, page, "T-2"), `class="agent"`) {
		t.Errorf("no agent session worked on T-2, which should have no band")
	}
}

func TestTheTimelineSaysThereIsNothingToDrawYetWithAnEmptyLedger(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()

	page := get(t, ui, "/timeline")
	wantText(t, page, "Nothing to draw yet: the Ledger has no Status change")
	if strings.Contains(page, `<li id="row-`) {
		t.Errorf("an empty Ledger should draw no row:\n%s", text(page))
	}
}

func TestTheTimelineOfATrackerKeptBacklogSaysMovesMadeInTheTrackerArentInTheLedger(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
	}})
	p.MustRun("move", "T-41", "in-progress")
	ui := p.StartUI()

	page := get(t, ui, "/timeline")
	wantText(t, page, "Ticket is kept in a tracker: moves made in the tracker itself aren't in the Ledger")
	ownBar(t, page, "T-41")

	if files := get(t, ticketPlaybook(t).StartUI(), "/timeline"); strings.Contains(text(files), "kept in a tracker") {
		t.Errorf("a backlog kept in files should say nothing about a tracker:\n%s", text(files))
	}
}

func TestTheTimelineAnswersOnlyThisMachineAndUpdatesItself(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()

	req := ui.NewRequest("GET", "/timeline", nil)
	req.Host = "attacker.example:80"
	if page := ui.Do(req); page.Status != http.StatusForbidden {
		t.Errorf("GET /timeline for attacker.example: status %d, want 403", page.Status)
	}
	page := get(t, ui, "/timeline")
	if !listens(page) || !strings.Contains(page, `var here = "/timeline"`) {
		t.Errorf("/timeline should reload itself when the project changes")
	}
	if strings.Contains(page, "<form") {
		t.Errorf("the Timeline only draws what happened, and offers no form:\n%s", text(page))
	}

	s := listenAsPage(t, ui, page)
	p.MustRun("create", "Ticket", "--title", "Login page")
	wantSignal(t, s, "a new Ticket")
	ownBar(t, get(t, ui, "/timeline"), "T-1")
}
