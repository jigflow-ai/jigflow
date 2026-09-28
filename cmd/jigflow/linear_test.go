package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakelinear"
)

// linearPlaybook is trackerPlaybook with its Tickets kept in the fake
// Linear's team ENG, labelled ticket, through the Linear Connector, and
// its Statuses mapped to the team's workflow states.
func linearPlaybook(t *testing.T) (*clitest.Project, *fakelinear.Server) {
	t.Helper()
	ln := fakelinear.New(t, 41)
	t.Setenv("LINEAR_API_KEY", fakelinear.Key)
	t.Setenv("LINEAR_API_URL", ln.URL+"/graphql")
	p := trackerPlaybook(t)
	p.Write(".jigflow/playbook.yaml", `name: team
connectors:
  tracker:
    command: `+linearConnector+`
    settings: {team: ENG, label: ticket, create_labels: true}
    types:
      Ticket:
        statuses:
          needs-triage: {state: Backlog}
          ready-for-agent: {state: Todo}
          in-progress: {state: In Progress}
          in-review: {state: In Review}
          done: {state: Done}
        fields:
          category:
            enhancement: {label: "kind: feature"}
`)
	return p, ln
}

func TestTicketsKeptInLinearGoThroughTheirWorkflow(t *testing.T) {
	p, ln := linearPlaybook(t)
	ln.Add(fakelinear.Issue{Title: "Reset-token table", State: "Todo", Labels: []string{"ticket"}})

	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--status", "ready-for-agent", "--field", "category=enhancement", "--link", "blocked_by=T-41")
	if i := ln.Issue(t, 42); i.State != "Todo" || fmt.Sprint(i.Labels) != "[kind: feature ticket]" || fmt.Sprint(i.BlockedBy) != "[41]" {
		t.Errorf("issue 42 is %+v, want it in Todo, labelled kind: feature and ticket, blocked by 41", i)
	}
	if first, all := agentNext(t, p); !strings.Contains(first, "T-41") || !strings.Contains(all, `T-42: not ready: waiting until every "blocked_by" item is done`) {
		t.Errorf("next should offer T-41 and hold T-42 back until T-41 is done:\n%s", all)
	}

	agentMove(t, p, "T-41", "in-progress", 0)
	if i := ln.Issue(t, 41); i.State != "In Progress" || i.Assignee != "jfl-bot" {
		t.Errorf("issue 41 after agent A's move is %+v, want it In Progress and assigned", i)
	}
	if r := p.RunInSession("B", "move", "T-41", "in-review"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "T-41 is claimed by agent session A.") {
		t.Errorf("agent B's move of A's Claim exited %d: %s", r.ExitCode, r.Stderr)
	}
	agentMove(t, p, "T-41", "in-review", 0)
	p.MustRun("move", "T-41", "done")
	if i := ln.Issue(t, 41); i.State != "Done" || i.Assignee != "" || fmt.Sprint(i.Labels) != "[ticket]" {
		t.Errorf("issue 41 when done is %+v, want it Done, unassigned, with only the ticket label", i)
	}
	if r := p.MustRun("query"); !strings.Contains(r.Stdout, `T-41 Ticket "Reset-token table": done`) || !strings.Contains(r.Stdout, `T-42 Ticket "Reset endpoint": ready-for-agent, category: enhancement`) {
		t.Errorf("query:\n%s", r.Stdout)
	}
	if first, _ := agentNext(t, p); !strings.Contains(first, "T-42") {
		t.Errorf("next once T-41 is done = %q, want T-42", first)
	}
}

func TestAnIssueFiledInLinearIsAnArtifactInTheStatusOfItsWorkflowState(t *testing.T) {
	p, ln := linearPlaybook(t)
	ln.Add(fakelinear.Issue{Title: "Crash on login", State: "In Review", Labels: []string{"ticket"}})
	ln.Add(fakelinear.Issue{Title: "Idea", State: "Canceled", Labels: []string{"ticket"}})

	if r := p.MustRun("query"); !strings.Contains(r.Stdout, `T-41 Ticket "Crash on login": in-review`) || !strings.Contains(r.Stdout, `T-42 Ticket "Idea": needs-triage`) {
		t.Errorf("query:\n%s", r.Stdout)
	}
}

func TestLinearRateLimitingIsATrackerProblem(t *testing.T) {
	p, ln := linearPlaybook(t)
	ln.Add(fakelinear.Issue{Title: "Reset-token table", Labels: []string{"ticket"}})
	ln.RateLimit()

	r := p.Run("move", "T-41", "in-progress")
	if want := `jfl move: tracker problem, not a workflow refusal: Connector "tracker" was rate-limited by the tracker: Rate limit exceeded. Retry in 42s.`; r.ExitCode != 3 || !strings.HasPrefix(r.Stderr, want) {
		t.Errorf("move exited %d with %q, want 3 and %q", r.ExitCode, r.Stderr, want)
	}
}
