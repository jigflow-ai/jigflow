package main_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakelinear"
)

// githubDoing is githubPlaybook with three Tickets in acme/shop: T-41 and
// T-42 in-progress, labelled status: doing, and T-43 ready-for-agent, an
// enhancement, labelled kind: feature.
func githubDoing(t *testing.T) (*clitest.Project, *fakegithub.Server) {
	t.Helper()
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "status: doing"}})
	gh.Add(fakegithub.Issue{Title: "Reset endpoint", Labels: []string{"ticket", "status: doing"}})
	gh.Add(fakegithub.Issue{Title: "Reset email", Labels: []string{"ticket", "ready-for-agent", "kind: feature"}})
	return p, gh
}

// queried is what jfl query says of every Artifact.
const queried = `T-41 Ticket "Reset-token table": in-progress
T-42 Ticket "Reset endpoint": in-progress
T-43 Ticket "Reset email": ready-for-agent, category: enhancement
`

func TestRemappingAStatusToAnotherGitHubLabelSaysHowManyCarryTheOldOneAndApprovingRelabelsThem(t *testing.T) {
	p, gh := githubDoing(t)
	if got := p.MustRun("query").Stdout; got != queried {
		t.Fatalf("query before the remap:\n%s", got)
	}
	p.Write("doing.yaml", "summary: in-progress is doing\nitems:\n  - {connector: tracker, types: {Ticket: {statuses: {in-progress: doing}}}}\n")

	const carry = `Ticket in-progress from label "status: doing" to label "doing": 2 Artifacts carry the old one and are relabelled: T-41, T-42`
	r := p.RunInSession("A", "propose", "doing.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, `1. change Connector "tracker": Ticket status in-progress: doing`) || !strings.Contains(r.Stdout, carry) {
		t.Fatalf("proposing the remap exited %d, want it to say %q:\n%s%s", r.ExitCode, carry, r.Stdout, r.Stderr)
	}
	if got := p.MustRun("check", "--proposal", "P-1").Stdout; !strings.Contains(got, "as P-1 would make it: no problems") {
		t.Errorf("check --proposal P-1 printed:\n%s", got)
	}
	if got := p.MustRun("show", "P-1").Stdout; !strings.Contains(got, `Ticket status in-progress: doing (replacing Ticket status in-progress: status: doing)`) {
		t.Errorf("jfl show P-1 should say the mapping it replaces:\n%s", got)
	}
	if i := gh.Issue(t, 41); !slices.Contains(i.Labels, "status: doing") {
		t.Errorf("a pending Proposal relabelled issue 41: %v", i.Labels)
	}

	term := p.StartInTerminal("approve", "P-1")
	term.Expect(carry)
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 || !strings.Contains(r.Output, "approved P-1") {
		t.Fatalf("approving P-1 exited %d:\n%s", r.ExitCode, r.Output)
	}
	for _, n := range []int{41, 42} {
		if i := gh.Issue(t, n); fmt.Sprint(i.Labels) != "[ticket doing]" {
			t.Errorf("issue %d is labelled %v, want ticket and doing", n, i.Labels)
		}
	}
	if got := p.MustRun("query").Stdout; got != queried {
		t.Errorf("every Artifact should read back as before the remap:\n%s", got)
	}
	if got := p.Read(".jigflow/playbook.yaml"); !strings.Contains(got, "in-progress: doing") {
		t.Errorf("the Playbook file should map in-progress to doing:\n%s", got)
	}
}

func TestARelabelFailingPartwayPutsBackTheArtifactsRelabelledAndThePlaybookFileAndIsATrackerProblem(t *testing.T) {
	p, gh := githubDoing(t)
	p.Write("doing.yaml", "summary: in-progress is doing\nitems:\n  - {connector: tracker, types: {Ticket: {statuses: {in-progress: doing}}}}\n")
	p.MustRun("propose", "doing.yaml")
	file := p.Read(".jigflow/playbook.yaml")
	gh.RateLimitChangesTo(42)

	term := p.StartInTerminal("approve", "P-1")
	term.Expect("[y/N] ")
	term.Type("y\n")
	r := term.Wait()
	if r.ExitCode != 3 || !strings.Contains(r.Output, "jfl approve: tracker problem, not a workflow refusal: ") || !strings.Contains(r.Output, "relabelling T-42") || !strings.Contains(r.Output, "T-41 was put back") {
		t.Errorf("approving while issue 42 can't be relabelled exited %d, want 3, a tracker problem, and T-41 put back:\n%s", r.ExitCode, r.Output)
	}
	for _, n := range []int{41, 42} {
		if i := gh.Issue(t, n); fmt.Sprint(i.Labels) != "[ticket status: doing]" {
			t.Errorf("issue %d is labelled %v, want ticket and status: doing as before", n, i.Labels)
		}
	}
	if got := p.Read(".jigflow/playbook.yaml"); got != file {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, file)
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
	if got := p.MustRun("query").Stdout; got != queried {
		t.Errorf("every Artifact should read as before:\n%s", got)
	}
}

// linearTriage is linearPlaybook with three Tickets in ENG: T-41
// needs-triage, in Backlog, T-42 ready-for-agent, in Todo, an enhancement,
// labelled kind: feature, and T-43 in-progress, in In Progress, an
// enhancement too.
func linearTriage(t *testing.T) (*clitest.Project, *fakelinear.Server) {
	t.Helper()
	p, ln := linearPlaybook(t)
	ln.Add(fakelinear.Issue{Title: "Reset-token table", State: "Backlog", Labels: []string{"ticket"}})
	ln.Add(fakelinear.Issue{Title: "Reset endpoint", State: "Todo", Labels: []string{"ticket", "kind: feature"}})
	ln.Add(fakelinear.Issue{Title: "Reset email", State: "In Progress", Labels: []string{"ticket", "kind: feature"}})
	return p, ln
}

// triaged is what jfl query says of linearTriage's Artifacts.
const triaged = `T-41 Ticket "Reset-token table": needs-triage
T-42 Ticket "Reset endpoint": ready-for-agent, category: enhancement
T-43 Ticket "Reset email": in-progress, category: enhancement
`

func TestRemappingAStatusAndFieldValuesInLinearRelabelsTheArtifactsCarryingTheOldOnesOnly(t *testing.T) {
	p, ln := linearTriage(t)
	if got := p.MustRun("query").Stdout; got != triaged {
		t.Fatalf("query before the remap:\n%s", got)
	}
	p.Write("triage.yaml", `summary: label triage and kinds
items:
  - connector: tracker
    types:
      Ticket:
        statuses: {needs-triage: {label: triage, state: Backlog}}
        fields: {category: {enhancement: feature, bug: "kind: bug"}}
`)

	lines := []string{
		`Ticket needs-triage from state "Backlog" to label "triage" and state "Backlog": 1 Artifact carries the old one and is relabelled: T-41`,
		`Ticket category bug from label "bug" to label "kind: bug": no Artifact carries the old one, so none is relabelled`,
		`Ticket category enhancement from label "kind: feature" to label "feature": 2 Artifacts carry the old one and are relabelled: T-42, T-43`,
	}
	r := p.RunInSession("A", "propose", "triage.yaml")
	for _, line := range lines {
		if r.ExitCode != 0 || !strings.Contains(r.Stdout, line) {
			t.Errorf("proposing the remap exited %d, want it to say %q:\n%s%s", r.ExitCode, line, r.Stdout, r.Stderr)
		}
	}
	if r := approveInTerminal(t, p, "P-1"); !strings.Contains(r.Output, lines[2]) {
		t.Errorf("jfl approve should say %q before asking:\n%s", lines[2], r.Output)
	}
	if i := ln.Issue(t, 41); i.State != "Backlog" || fmt.Sprint(i.Labels) != "[ticket triage]" {
		t.Errorf("issue 41 is %+v, want it in Backlog, labelled ticket and triage", i)
	}
	for _, n := range []int{42, 43} {
		if i := ln.Issue(t, n); fmt.Sprint(i.Labels) != "[ticket feature]" {
			t.Errorf("issue %d is labelled %v, want ticket and feature", n, i.Labels)
		}
	}
	if got := p.MustRun("query").Stdout; got != triaged {
		t.Errorf("every Artifact should read back as before the remap:\n%s", got)
	}
}

func TestARemapNoArtifactCarriesAppliesWithNothingToRelabel(t *testing.T) {
	p, gh := githubDoing(t)
	p.Write("review.yaml", "summary: in-review is review\nitems:\n  - {connector: tracker, types: {Ticket: {statuses: {in-review: review}}}}\n")
	before := len(gh.Requests())

	const none = `Ticket in-review from label "in-review" to label "review": no Artifact carries the old one, so none is relabelled`
	if r := p.RunInSession("A", "propose", "review.yaml"); r.ExitCode != 0 || !strings.Contains(r.Stdout, none) {
		t.Fatalf("proposing the remap exited %d, want it to say %q:\n%s%s", r.ExitCode, none, r.Stdout, r.Stderr)
	}
	if r := approveInTerminal(t, p, "P-1"); !strings.Contains(r.Output, none) {
		t.Errorf("jfl approve should say %q:\n%s", none, r.Output)
	}
	for _, req := range gh.Requests()[before:] {
		if !strings.HasPrefix(req, "GET ") {
			t.Errorf("a remap no Artifact carries changed the tracker: %s", req)
		}
	}
	if got := p.Read(".jigflow/playbook.yaml"); !strings.Contains(got, "in-review: review") {
		t.Errorf("the Playbook file should map in-review to review:\n%s", got)
	}
	if got := p.MustRun("query").Stdout; got != queried {
		t.Errorf("every Artifact should read as before:\n%s", got)
	}
}

func TestALinearRelabelFailingPartwayPutsBackTheArtifactsRelabelledAndThePlaybookFile(t *testing.T) {
	p, ln := linearTriage(t)
	p.Write("feature.yaml", "summary: kinds\nitems:\n  - {connector: tracker, types: {Ticket: {fields: {category: {enhancement: feature}}}}}\n")
	p.MustRun("propose", "feature.yaml")
	file := p.Read(".jigflow/playbook.yaml")
	ln.RateLimitChangesTo(43)

	term := p.StartInTerminal("approve", "P-1")
	term.Expect("[y/N] ")
	term.Type("y\n")
	r := term.Wait()
	if r.ExitCode != 3 || !strings.Contains(r.Output, "jfl approve: tracker problem, not a workflow refusal: ") || !strings.Contains(r.Output, "relabelling T-43") || !strings.Contains(r.Output, "T-42 was put back") {
		t.Errorf("approving while issue 43 can't be relabelled exited %d, want 3, a tracker problem, and T-42 put back:\n%s", r.ExitCode, r.Output)
	}
	for _, n := range []int{42, 43} {
		if i := ln.Issue(t, n); fmt.Sprint(i.Labels) != "[ticket kind: feature]" {
			t.Errorf("issue %d is labelled %v, want ticket and kind: feature as before", n, i.Labels)
		}
	}
	if got := p.Read(".jigflow/playbook.yaml"); got != file {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, file)
	}
	if got := p.MustRun("query").Stdout; got != triaged {
		t.Errorf("every Artifact should read as before:\n%s", got)
	}
}

func TestAMappingTheChecksRefuseIsNotProposedAndWritesNothing(t *testing.T) {
	p, gh := githubDoing(t)
	before := playbookTree(t, p)
	for file, want := range map[string]string{
		"shipped.yaml:{statuses: {shipped: done}}":            `Connector "tracker" maps Status "shipped", which a Ticket doesn't declare`,
		"nothing.yaml:{statuses: {done: {}}}":                 `Connector "tracker" maps Status "done" to no label or state`,
		"colour.yaml:{statuses: {done: {colour: red}}}":       `unknown field "colour" (want label or state)`,
		"size.yaml:{fields: {size: {large: big}}}":            `Connector "tracker" maps field "size", which a Ticket doesn't declare`,
		"value.yaml:{fields: {category: {chore: \"chore\"}}}": `Connector "tracker" maps category "chore", which isn't one of its values (bug, enhancement)`,
	} {
		name, ticket, _ := strings.Cut(file, ":")
		p.Write(name, "summary: remap\nitems:\n  - {connector: tracker, types: {Ticket: "+ticket+"}}\n")
		if r := p.RunInSession("A", "propose", name); r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("proposing %s exited %d, want a refusal saying %q: %s", ticket, r.ExitCode, want, r.Stderr)
		}
	}
	if after := playbookTree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused remap changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
	for _, n := range []int{41, 42} {
		if i := gh.Issue(t, n); !slices.Contains(i.Labels, "status: doing") {
			t.Errorf("a refused remap relabelled issue %d: %v", n, i.Labels)
		}
	}
}

func TestAPendingRemapSaysOnItsPageHowManyArtifactsCarryTheOldLabelAndWhich(t *testing.T) {
	p, _ := githubDoing(t)
	p.Write("doing.yaml", "summary: in-progress is doing\nitems:\n  - {connector: tracker, types: {Ticket: {statuses: {in-progress: doing, in-review: review}}}}\n")
	p.MustRun("propose", "doing.yaml")
	ui := p.StartUI()

	for _, page := range []string{get(t, ui, "/"), look(t, ui, "/")} {
		proposals := section(t, page, "Pending Proposals")
		wantText(t, proposals,
			`Ticket in-progress from label "status: doing" to label "doing": 2 Artifacts carry the old one and are relabelled`,
			`Ticket in-review from label "in-review" to label "review": no Artifact carries the old one, so none is relabelled`)
		for _, id := range []string{"T-41", "T-42"} {
			if !strings.Contains(proposals, `href="/artifacts/`+id+`"`) {
				t.Errorf("the Proposal should name %s, linking its page:\n%s", id, proposals)
			}
		}
	}
}

func TestWithTheKeyThePlaybookPageEditsEachStatusAndFieldValuesLabelAndStateAndSavingRelabels(t *testing.T) {
	p, gh := githubDoing(t)
	ui := p.StartUI()

	wantText(t, section(t, look(t, ui, "/playbook"), "Connectors"),
		"Ticket status needs-triage label needs-triage", "Ticket status in-progress label status: doing", "Ticket status done state closed",
		"Ticket category enhancement label kind: feature")
	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	for field, want := range map[string]string{
		"type.Ticket.status.needs-triage.label": "needs-triage", "type.Ticket.status.needs-triage.state": "",
		"type.Ticket.status.in-progress.label": "status: doing", "type.Ticket.status.in-progress.state": "",
		"type.Ticket.status.done.label": "", "type.Ticket.status.done.state": "closed",
		"type.Ticket.field.category.bug.label": "bug", "type.Ticket.field.category.enhancement.label": "kind: feature",
	} {
		if got, ok := form[field]; !ok || got[0] != want {
			t.Errorf("the tracker form sends %s = %q, want %q", field, got, want)
		}
	}

	form.Set("type.Ticket.status.in-progress.label", "doing")
	form.Set("type.Ticket.field.category.enhancement.label", "feature")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("saving the tracker Connector: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"),
		`approved P-1 as one unit: change Connector "tracker": Ticket status in-progress: doing; Ticket category enhancement: feature`,
		`Ticket in-progress from label "status: doing" to label "doing": 2 Artifacts carry the old one and are relabelled: T-41, T-42`,
		`Ticket category enhancement from label "kind: feature" to label "feature": 1 Artifact carries the old one and is relabelled: T-43`)
	if _, form := connectorForm(t, page.HTML, "tracker", "Save"); form.Get("type.Ticket.status.in-progress.label") != "doing" {
		t.Errorf("after saving, the tracker form sends %v", form)
	}
	if i := gh.Issue(t, 43); fmt.Sprint(i.Labels) != "[ticket ready-for-agent feature]" {
		t.Errorf("issue 43 is labelled %v, want ticket, ready-for-agent and feature", i.Labels)
	}
	if got := p.MustRun("query").Stdout; got != queried {
		t.Errorf("every Artifact should read back as before the remap:\n%s", got)
	}
}

func TestSavingAMappingOnThePlaybookPageTheTrackerCantRelabelIsATrackerProblemAndChangesNothing(t *testing.T) {
	p, gh := githubDoing(t)
	ui := p.StartUI()
	before := playbookTree(t, p)
	gh.RateLimitChangesTo(42)

	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	form.Set("type.Ticket.status.in-progress.label", "doing")
	page := ui.Post(action, form)
	if page.Status != http.StatusBadGateway {
		t.Fatalf("saving a mapping the tracker can't relabel: status %d, want 502\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Not done"), "tracker problem, not a workflow refusal", "relabelling T-42", "T-41 was put back")
	for _, n := range []int{41, 42} {
		if i := gh.Issue(t, n); fmt.Sprint(i.Labels) != "[ticket status: doing]" {
			t.Errorf("issue %d is labelled %v, want ticket and status: doing as before", n, i.Labels)
		}
	}
	after := playbookTree(t, p)
	maps.DeleteFunc(after, func(path, _ string) bool { return strings.Contains(path, "proposals") })
	maps.DeleteFunc(before, func(path, _ string) bool { return strings.Contains(path, "proposals") })
	if !maps.Equal(after, before) {
		t.Errorf("a change the tracker couldn't relabel for changed the Playbook:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
}
