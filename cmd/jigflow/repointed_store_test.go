package main_test

import (
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakelinear"
)

// githubShopAndWeb is githubPlaybook with two Tickets in acme/shop, T-41
// and T-42, and a repository acme/web whose own issue 41 is another
// Ticket, with another title.
func githubShopAndWeb(t *testing.T) (*clitest.Project, *fakegithub.Server) {
	t.Helper()
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket"}})
	gh.Add(fakegithub.Issue{Title: "Reset endpoint", Labels: []string{"ticket"}})
	gh.AddRepo("acme/web")
	gh.Add(fakegithub.Issue{Repo: "acme/web", Number: 41, Title: "Checkout page", Labels: []string{"ticket"}})
	return p, gh
}

func TestProposingAnotherGitHubRepoSaysWhichArtifactsWillNoLongerBeSeenAndApprovingStillAppliesIt(t *testing.T) {
	p, _ := githubShopAndWeb(t)
	p.Write("web.yaml", "summary: move to acme/web\nitems:\n  - {connector: tracker, settings: {repo: acme/web}}\n")

	// acme/web's own 41 is another Artifact than the T-41 seen now.
	const unseen = "2 Artifacts will no longer be seen: T-41, T-42"
	r := p.RunInSession("A", "propose", "web.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, unseen) {
		t.Fatalf("proposing another repo exited %d, want it to say %q:\n%s%s", r.ExitCode, unseen, r.Stdout, r.Stderr)
	}
	term := p.StartInTerminal("approve", "P-1")
	term.Expect(unseen)
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("approving P-1 exited %d:\n%s", r.ExitCode, r.Output)
	}
	if got := p.Read(".jigflow/playbook.yaml"); !strings.Contains(got, "repo: acme/web") {
		t.Errorf("the Playbook file should keep Tickets in acme/web:\n%s", got)
	}
	if got := p.MustRun("query").Stdout; !strings.Contains(got, `T-41 Ticket "Checkout page"`) || strings.Contains(got, "Reset") {
		t.Errorf("query should list acme/web's Tickets only:\n%s", got)
	}
}

func TestProposingAnotherLinearTeamSaysWhichArtifactsWillNoLongerBeSeenAndApprovingStillAppliesIt(t *testing.T) {
	p, ln := linearPlaybook(t)
	ln.Add(fakelinear.Issue{Title: "Reset-token table", Labels: []string{"ticket"}})
	ln.Add(fakelinear.Issue{Title: "Reset endpoint", Labels: []string{"ticket"}})
	ln.AddTeam("OPS")
	ln.Add(fakelinear.Issue{Team: "OPS", Title: "On-call rota", Labels: []string{"ticket"}})
	p.Write("ops.yaml", "summary: move to OPS\nitems:\n  - {connector: tracker, settings: {team: OPS}}\n")

	const unseen = "2 Artifacts will no longer be seen: T-41, T-42"
	r := p.RunInSession("A", "propose", "ops.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, unseen) {
		t.Fatalf("proposing another team exited %d, want it to say %q:\n%s%s", r.ExitCode, unseen, r.Stdout, r.Stderr)
	}
	if r := approveInTerminal(t, p, "P-1"); !strings.Contains(r.Output, unseen) {
		t.Errorf("jfl approve should say %q before asking:\n%s", unseen, r.Output)
	}
	if got := p.MustRun("query").Stdout; !strings.Contains(got, `T-43 Ticket "On-call rota"`) || strings.Contains(got, "Reset") {
		t.Errorf("query should list OPS's Tickets only:\n%s", got)
	}
}

// githubTicketsAndBugs is githubPlaybook whose Connector keeps Bugs too,
// labelled bug, in acme/shop: Tickets T-41 and T-42, the latter labelled
// task too, and Bug B-43.
func githubTicketsAndBugs(t *testing.T) *clitest.Project {
	t.Helper()
	p, gh := githubPlaybook(t)
	p.Write(".jigflow/playbook.yaml", strings.Replace(p.Read(".jigflow/playbook.yaml"), "    types:\n", "    types:\n      Bug:\n        settings: {label: bug}\n", 1))
	p.Write(".jigflow/types/bug.yaml", "name: Bug\nprefix: B\nstore: tracker\nstatuses: [open, fixed]\ninitial: [open]\nfinal: [fixed]\ntransitions:\n  - {from: open, to: fixed}\n")
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket"}})
	gh.Add(fakegithub.Issue{Title: "Reset endpoint", Labels: []string{"ticket", "task"}})
	gh.Add(fakegithub.Issue{Title: "Crash on logout", Labels: []string{"bug"}})
	p.MustRun("check")
	return p
}

func TestProposingAnotherLabelForATypeSaysWhichOfItsArtifactsOnlyWillNoLongerBeSeen(t *testing.T) {
	p := githubTicketsAndBugs(t)
	p.Write("task.yaml", "summary: Tickets are tasks\nitems:\n  - {connector: tracker, types: {Ticket: {settings: {label: task}}}}\n")

	r := p.RunInSession("A", "propose", "task.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "1 Artifact will no longer be seen: T-41\n") {
		t.Fatalf("proposing another label for Tickets exited %d, want it to say T-41 alone will no longer be seen:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	approveInTerminal(t, p, "P-1")
	if got := p.MustRun("query").Stdout; strings.Contains(got, "T-41") || !strings.Contains(got, "T-42") || !strings.Contains(got, "B-43") {
		t.Errorf("query should list T-42 and B-43 only:\n%s", got)
	}
}

func TestASettingsChangeThatLeavesEveryArtifactSeenSaysNothingOfIt(t *testing.T) {
	p := githubTicketsAndBugs(t)
	p.Write("labels.yaml", "summary: no new labels\nitems:\n  - {connector: tracker, settings: {create_labels: false}}\n")

	r := p.RunInSession("A", "propose", "labels.yaml")
	if r.ExitCode != 0 || strings.Contains(r.Stdout, "seen") {
		t.Fatalf("proposing create_labels exited %d, want it to say nothing of Artifacts no longer seen:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if r := approveInTerminal(t, p, "P-1"); strings.Contains(r.Output, "seen") {
		t.Errorf("jfl approve should say nothing of Artifacts no longer seen:\n%s", r.Output)
	}
}

func TestAStoreThatCantBeListedAsTheProposalWouldMakeItIsATrackerProblemAndNothingIsApplied(t *testing.T) {
	p, gh := githubShopAndWeb(t)
	p.Write("nowhere.yaml", "summary: move to acme/nowhere\nitems:\n  - {connector: tracker, settings: {repo: acme/nowhere}}\n")
	p.Write("web.yaml", "summary: move to acme/web\nitems:\n  - {connector: tracker, settings: {repo: acme/web}}\n")
	before := playbookTree(t, p)

	r := p.RunInSession("A", "propose", "nowhere.yaml")
	if r.ExitCode != 3 || !strings.HasPrefix(r.Stderr, "jfl propose: tracker problem, not a workflow refusal: ") || !strings.Contains(r.Stderr, "check that the repository exists") {
		t.Errorf("proposing a repo that can't be listed exited %d, want 3 and a tracker problem: %s", r.ExitCode, r.Stderr)
	}
	if after := playbookTree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused Proposal changed the project")
	}

	p.MustRun("propose", "web.yaml")
	file := p.Read(".jigflow/playbook.yaml")
	gh.RateLimit()
	if r := p.Run("approve", "P-1"); r.ExitCode != 3 || !strings.Contains(r.Stderr, "tracker problem, not a workflow refusal") {
		t.Errorf("approving while the tracker can't be listed exited %d, want 3 and a tracker problem: %s", r.ExitCode, r.Stderr)
	}
	if got := p.Read(".jigflow/playbook.yaml"); got != file {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, file)
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}

func TestAPendingProposalRePointingAStoreSaysOnItsPageWhichArtifactsWillNoLongerBeSeen(t *testing.T) {
	p, _ := githubShopAndWeb(t)
	p.Write("web.yaml", "summary: move to acme/web\nitems:\n  - {connector: tracker, settings: {repo: acme/web}}\n")
	p.MustRun("propose", "web.yaml")
	ui := p.StartUI()

	for _, page := range []string{get(t, ui, "/"), look(t, ui, "/")} {
		proposals := section(t, page, "Pending Proposals")
		wantText(t, proposals, `change Connector "tracker": setting repo: acme/web replacing setting repo: acme/shop 2 Artifacts will no longer be seen`)
		for _, id := range []string{"T-41", "T-42"} {
			if !strings.Contains(proposals, `href="/artifacts/`+id+`"`) {
				t.Errorf("the Proposal should name %s, linking its page:\n%s", id, proposals)
			}
		}
	}
}

func TestSavingARepoThatCantBeListedOnThePlaybookPageIsATrackerProblemAndChangesNoFile(t *testing.T) {
	p, _ := githubShopAndWeb(t)
	ui := p.StartUI()
	before := playbookTree(t, p)

	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	form.Set("setting.repo", "acme/nowhere")
	page := ui.Post(action, form)
	if page.Status != http.StatusBadGateway {
		t.Fatalf("saving a repo that can't be listed: status %d, want 502\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Not done"), "tracker problem, not a workflow refusal", "check that the repository exists")
	if after := playbookTree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused change changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
}

func TestSavingAnotherRepoOnThePlaybookPageSaysWhichArtifactsAreNoLongerSeen(t *testing.T) {
	p, _ := githubShopAndWeb(t)
	ui := p.StartUI()

	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	form.Set("setting.repo", "acme/web")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("saving another repo: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `approved P-1 as one unit: change Connector "tracker": setting repo: acme/web 2 Artifacts will no longer be seen: T-41, T-42`)
}
