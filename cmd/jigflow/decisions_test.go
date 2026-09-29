package main_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

var (
	forms   = regexp.MustCompile(`(?s)<form\b([^>]*)>(.*?)</form>`)
	attr    = regexp.MustCompile(`(?s)\b(\w+)="([^"]*)"`)
	inputs  = regexp.MustCompile(`(?s)<input\b([^>]*)>`)
	selects = regexp.MustCompile(`(?s)<select\b([^>]*)>(.*?)</select>`)
	options = regexp.MustCompile(`(?s)<option\b([^>]*)>`)
	areas   = regexp.MustCompile(`(?s)<textarea\b([^>]*)>(.*?)</textarea>`)
	buttons = regexp.MustCompile(`(?s)<button\b([^>]*)>(.*?)</button>`)
)

// attrs returns the attributes of an HTML tag, by name.
func attrs(tag string) map[string]string {
	m := map[string]string{}
	for _, a := range attr.FindAllStringSubmatch(tag, -1) {
		m[a[1]] = html.UnescapeString(a[2])
	}
	return m
}

// hasForm reports whether fragment has a form with a button a person reads
// as button.
func hasForm(fragment, button string) bool {
	_, _, ok := findForm(fragment, button)
	return ok
}

// findForm returns where the form with the button a person reads as button
// posts to, and what submitting it with that button sends as it is.
func findForm(fragment, button string) (string, url.Values, bool) {
	for _, f := range forms.FindAllStringSubmatch(fragment, -1) {
		values := url.Values{}
		pressed := false
		for _, b := range buttons.FindAllStringSubmatch(f[2], -1) {
			if text(b[2]) == button {
				pressed = true
				if a := attrs(b[1]); a["name"] != "" {
					values.Set(a["name"], a["value"])
				}
			}
		}
		if !pressed {
			continue
		}
		for _, in := range inputs.FindAllStringSubmatch(f[2], -1) {
			if a := attrs(in[1]); a["name"] != "" {
				values.Add(a["name"], a["value"])
			}
		}
		for _, s := range selects.FindAllStringSubmatch(f[2], -1) {
			name := attrs(s[1])["name"]
			for i, o := range options.FindAllStringSubmatch(s[2], -1) {
				a := attrs(o[1])
				if i == 0 || strings.Contains(o[1], "selected") {
					values.Set(name, a["value"])
				}
			}
		}
		for _, ta := range areas.FindAllStringSubmatch(f[2], -1) {
			if name := attrs(ta[1])["name"]; name != "" {
				values.Set(name, html.UnescapeString(ta[2]))
			}
		}
		return attrs(f[1])["action"], values, true
	}
	return "", nil, false
}

// submit presses the button a person reads as button in fragment, a page of
// the Dashboard, after changing the form's fields as edits says, and returns
// the page the Dashboard answers with.
func submit(t *testing.T, ui *clitest.UI, fragment, button string, edits url.Values) clitest.Page {
	t.Helper()
	action, values, ok := findForm(fragment, button)
	if !ok {
		t.Fatalf("no %q button to press in:\n%s", button, text(fragment))
	}
	for name, v := range edits {
		if !values.Has(name) {
			t.Fatalf("the %q form has no field %q to change; it has %v", button, name, values)
		}
		values[name] = v
	}
	return ui.Post(action, values)
}

func TestAPersonApprovesAPendingProposalInTheDashboard(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUI()

	proposals := section(t, get(t, ui, "/"), "Pending Proposals")
	page := submit(t, ui, proposals, "Approve all 4 changes", nil)
	if page.Status != http.StatusOK {
		t.Fatalf("approving P-1: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, page.HTML, "approved P-1 as one unit", `created T-1 "Reset-token table" in ready-for-agent`, "S-1: ready-for-agent → ticketed")
	if got := text(section(t, page.HTML, "Pending Proposals")); strings.Contains(got, "P-1") {
		t.Errorf("P-1 was approved, but is still listed as pending:\n%s", got)
	}
	q := p.MustRun("query").Stdout
	for _, want := range []string{
		`S-1 Spec "Password reset by email": ticketed`,
		`T-2 Ticket "Reset endpoint": ready-for-agent, blocked_by: T-1, part_of: S-1`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("jfl query should list %q:\n%s", want, q)
		}
	}
	if l := p.MustRun("ledger").Stdout; !strings.Contains(l, "Reset-token table") {
		t.Errorf("the approval should be in the Ledger:\n%s", l)
	}
}

func TestAPersonEditsItemsOfAProposalBeforeApprovingIt(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUI()

	proposals := section(t, get(t, ui, "/"), "Pending Proposals")
	page := submit(t, ui, proposals, "Approve all 4 changes", url.Values{
		"item-2-title":           {"Reset endpoint, rate-limited"},
		"item-2-link-blocked_by": {""},
		"item-3-link-blocked_by": {"token-table, S-1"},
	})
	if page.Status != http.StatusConflict {
		t.Fatalf("approving with a Link to a Spec as blocked_by: status %d, want 409\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Not done"), "P-1 was not applied at all", `item 3 (create Ticket "Email template", blocked_by token-table, S-1, part_of S-1)`)
	assertNothingApplied(t, p)

	proposals = section(t, page.HTML, "Pending Proposals")
	page = submit(t, ui, proposals, "Approve all 4 changes", url.Values{
		"item-2-title":           {"Reset endpoint, rate-limited"},
		"item-2-link-blocked_by": {""},
	})
	if page.Status != http.StatusOK {
		t.Fatalf("approving with edits: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `item 2 edited: create Ticket "Reset endpoint, rate-limited", part_of S-1`, "approved P-1 as one unit")
	q := p.MustRun("query", "--type", "Ticket").Stdout
	for _, want := range []string{
		`T-1 Ticket "Reset-token table": ready-for-agent, part_of: S-1`,
		`T-2 Ticket "Reset endpoint, rate-limited": ready-for-agent, part_of: S-1` + "\n",
		`T-3 Ticket "Email template": ready-for-agent, blocked_by: T-1, part_of: S-1`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("jfl query should list %q:\n%s", want, q)
		}
	}
}

func TestAPersonRejectsAPendingProposalInTheDashboard(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUI()

	page := submit(t, ui, section(t, get(t, ui, "/"), "Pending Proposals"), "Reject", nil)
	if page.Status != http.StatusOK {
		t.Fatalf("rejecting P-1: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), "rejected P-1. Nothing changed.")
	wantText(t, section(t, page.HTML, "Pending Proposals"), "No pending Proposals")
	assertNothingApplied(t, p)
	if first, _ := agentNext(t, p); first != `run /to-tickets on S-1 "Password reset by email"` {
		t.Errorf("after the rejection, next = %q, want S-1 again", first)
	}
}

func TestOnlyTheBrowserThatOpenedTheLinkJflUiPrintedMayDecide(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUI()
	approve, form, _ := findForm(section(t, get(t, ui, "/"), "Pending Proposals"), "Approve all 4 changes")

	// An agent's curl knows where the Dashboard serves, and may look...
	page := ui.Curl(ui.NewRequest(http.MethodGet, "/", nil))
	if page.Status != http.StatusOK {
		t.Fatalf("GET / without the link: status %d", page.Status)
	}
	if hasForm(page.HTML, "Approve all 4 changes") {
		t.Error("the page offers to approve to a program that never opened the link")
	}
	wantText(t, page.HTML, "open the link jfl ui printed in the terminal where you started it")
	// ...but not decide, even knowing what the person's form sends.
	for _, path := range []string{approve, "/proposals/P-1/reject"} {
		if page := ui.Curl(ui.NewRequest(http.MethodPost, path, form)); page.Status != http.StatusForbidden {
			t.Errorf("POST %s without the link: status %d, want 403", path, page.Status)
		}
	}
	// A page elsewhere can't make the person's browser decide.
	req := ui.NewRequest(http.MethodPost, approve, form)
	req.Header.Set("Origin", "http://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if page := ui.Do(req); page.Status != http.StatusForbidden {
		t.Errorf("a POST from another site: status %d, want 403", page.Status)
	}
	assertNothingApplied(t, p)
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}

func TestADashboardAnAgentSessionStartedOnlyShowsTheProject(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUIInSession("A")

	page := get(t, ui, "/")
	if hasForm(page, "Approve all 4 changes") {
		t.Error("a Dashboard agent session A started offers to approve")
	}
	wantText(t, page, "agent session A started this Dashboard")
	if page := ui.Post("/proposals/P-1/reject", url.Values{}); page.Status != http.StatusForbidden {
		t.Errorf("rejecting in a Dashboard an agent started: status %d, want 403", page.Status)
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}

// rowHTML returns the HTML of the table row of the Artifact id.
func rowHTML(t *testing.T, page, id string) string {
	t.Helper()
	_, rest, ok := strings.Cut(page, `<tr id="`+id+`"`)
	if !ok {
		t.Fatalf("the page has no row for %s:\n%s", id, text(page))
	}
	r, _, _ := strings.Cut(rest, "</tr>")
	return "<tr" + r + "</tr>"
}

func TestAPersonMakesAHumanTransitionInTheDashboard(t *testing.T) {
	p := mergePlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add signup page")
	ui := p.StartUI()

	page := get(t, ui, "/")
	if hasForm(section(t, page, "Waiting for you"), "→ done") == false {
		t.Error("the human queue doesn't offer T-1's Human Transition")
	}
	if hasForm(rowHTML(t, page, "T-2"), "→ ready-to-merge") {
		t.Error("T-2's row offers a Transition that isn't a Human Transition")
	}
	done := submit(t, ui, rowHTML(t, page, "T-1"), "→ done", nil)
	if done.Status != http.StatusOK {
		t.Fatalf("moving T-1 to done: status %d\n%s", done.Status, text(done.HTML))
	}
	wantText(t, section(t, done.HTML, "Done"), "T-1: ready-to-merge → done", `Action "commit" succeeded`)
	if got := p.Read("ran.log"); got != "gate\naction\n" {
		t.Errorf("ran.log = %q, want the Gate then the Action", got)
	}
	if q := p.MustRun("query", "--status", "done").Stdout; !strings.Contains(q, `T-1 Ticket "Add login page": done`) {
		t.Errorf("jfl query should list T-1 as done:\n%s", q)
	}
	if l := p.MustRun("ledger").Stdout; regexp.MustCompile(`ready-to-merge\s+\S+ so far`).MatchString(l) {
		t.Errorf("the Ledger should record that T-1 left ready-to-merge:\n%s", l)
	}
}

// dashboardMergePlaybook is mergePlaybook with its merge, ready-to-merge →
// done, a Human Transition the Playbook requires making in the Dashboard.
func dashboardMergePlaybook(t *testing.T) *clitest.Project {
	t.Helper()
	p := mergePlaybook(t)
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "human: true", "human: dashboard", 1))
	p.MustRun("check")
	return p
}

func TestATransitionThePlaybookRequiresTheDashboardForIsRefusedInATerminal(t *testing.T) {
	p := dashboardMergePlaybook(t)
	before := p.Read(".jigflow/state/T-1.md")

	r := p.StartInTerminal("move", "T-1", "done").Wait()
	if r.ExitCode != 1 || strings.Contains(r.Output, "[y/N]") {
		t.Errorf("the merge in a terminal exited %d, want 1 without asking; terminal:\n%s", r.ExitCode, r.Output)
	}
	want := `T-1: "ready-to-merge" → "done" is a Human Transition the Playbook requires making in the Dashboard: run jfl ui and make it there`
	if !strings.Contains(r.Output, want) {
		t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\n%s", after)
	}
	assertNothingRan(t, p)

	ui := p.StartUI()
	page := submit(t, ui, rowHTML(t, get(t, ui, "/"), "T-1"), "→ done", nil)
	if page.Status != http.StatusOK {
		t.Fatalf("the merge in the Dashboard: status %d\n%s", page.Status, text(page.HTML))
	}
	if q := p.MustRun("query", "--status", "done").Stdout; !strings.Contains(q, "T-1") {
		t.Errorf("T-1 should be done:\n%s", q)
	}
}

func TestAProposalMakingATransitionThatRequiresTheDashboardIsApprovedOnlyThere(t *testing.T) {
	p := dashboardMergePlaybook(t)
	p.Write("merge.yaml", "summary: merge T-1\nitems:\n  - {move: T-1, to: done}\n")
	if r := p.RunInSession("A", "propose", "merge.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}

	r := p.StartInTerminal("approve", "P-1").Wait()
	if r.ExitCode != 1 || strings.Contains(r.Output, "[y/N]") {
		t.Errorf("approving in a terminal exited %d, want 1 without asking; terminal:\n%s", r.ExitCode, r.Output)
	}
	want := `P-1 was not applied at all (all or nothing): item 1 (move T-1 → done): T-1: "ready-to-merge" → "done" is a Human Transition the Playbook requires making in the Dashboard: run jfl ui and approve P-1 there`
	if !strings.Contains(r.Output, want) {
		t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
	}
	assertNothingRan(t, p)

	ui := p.StartUI()
	page := submit(t, ui, section(t, get(t, ui, "/"), "Pending Proposals"), "Approve", nil)
	if page.Status != http.StatusOK {
		t.Fatalf("approving in the Dashboard: status %d\n%s", page.Status, text(page.HTML))
	}
	if q := p.MustRun("query", "--status", "done").Stdout; !strings.Contains(q, "T-1") {
		t.Errorf("T-1 should be done:\n%s", q)
	}
}

// An agent that shells out to decide on such a Proposal is sent to the
// Dashboard, not to the approve tool, which would refuse it without asking.
func TestAnAgentSessionsRefusedDecisionOnADashboardProposalNamesOnlyTheDashboard(t *testing.T) {
	p := dashboardMergePlaybook(t)
	p.Write("merge.yaml", "summary: merge T-1\nitems:\n  - {move: T-1, to: done}\n")
	if r := p.RunInSession("A", "propose", "merge.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}

	for _, cmd := range []string{"approve", "reject"} {
		r := p.RunInSession("A", cmd, "P-1")
		want := `Only a human can ` + cmd + ` P-1. P-1 waits for a person in the Dashboard: T-1: "ready-to-merge" → "done" is a Human Transition the Playbook requires making in the Dashboard: run jfl ui and approve P-1 there.`
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) || strings.Contains(r.Stderr, "approve tool") {
			t.Errorf("an agent's %s: exit %d, stderr %q; want %q, naming no tool", cmd, r.ExitCode, r.Stderr, want)
		}
	}
}

func TestTheWorkflowSaysWhichTransitionsTheDashboardIsRequiredFor(t *testing.T) {
	p := dashboardMergePlaybook(t)

	if out := p.MustRun("simulate", "Ticket").Stdout; !strings.Contains(out, "→ done; Human Transition, in the Dashboard only") {
		t.Errorf("simulate should mark the merge as made in the Dashboard only:\n%s", out)
	}
	ui := p.StartUI()
	ticket := section(t, get(t, ui, "/workflows"), "Ticket")
	wantText(t, svg(t, ticket), "ready-to-merge → done: Human Transition, in the Dashboard only")
	wantText(t, ticket, "ready-to-merge → done Human Transition, in the Dashboard only")
}

func TestTheDashboardNeverEditsArtifactBodies(t *testing.T) {
	p := mergePlaybook(t)
	edited := p.Read(".jigflow/state/T-1.md") + "\nMerge only after the release.\n"
	p.Write(".jigflow/state/T-1.md", edited)
	ui := p.StartUI()

	page := get(t, ui, "/")
	if strings.Contains(page, "<textarea") {
		t.Error("the backlog offers to edit text a body could be written in")
	}
	done := submit(t, ui, rowHTML(t, page, "T-1"), "→ done", nil)
	if done.Status != http.StatusOK {
		t.Fatalf("moving T-1 to done: status %d\n%s", done.Status, text(done.HTML))
	}
	// The body edited in the person's editor is re-validated and kept, as
	// jfl move keeps it.
	wantText(t, section(t, done.HTML, "Done"), "T-1: body edited outside jfl, re-validated")
	if got := p.Read(".jigflow/state/T-1.md"); !strings.HasSuffix(got, "\nMerge only after the release.\n") {
		t.Errorf("the move changed T-1's body:\n%s", got)
	}
}

func TestAHumanTransitionInTheDashboardReachesTheTrackerAndTellsTrackerProblemsApart(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 43, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"needs-triage"}},
		{"id": "42", "title": "Add signup page", "labels": []string{"needs-triage"}},
	}})
	ui := p.StartUI()

	page := get(t, ui, "/")
	if done := submit(t, ui, rowHTML(t, page, "T-41"), "→ ready-for-agent", nil); done.Status != http.StatusOK {
		t.Fatalf("moving T-41: status %d\n%s", done.Status, text(done.HTML))
	}
	if q := p.MustRun("query", "--status", "ready-for-agent").Stdout; !strings.Contains(q, `T-41 Ticket "Add login page": ready-for-agent`) {
		t.Errorf("the tracker should have T-41 in ready-for-agent:\n%s", q)
	}

	setTracker(t, p, map[string]any{"fail": "network"})
	failed := submit(t, ui, rowHTML(t, page, "T-42"), "→ ready-for-agent", nil)
	if failed.Status != http.StatusBadGateway {
		t.Errorf("moving T-42 with the tracker unreachable: status %d, want 502", failed.Status)
	}
	wantText(t, failed.HTML, "tracker problem, not a workflow refusal")
}
