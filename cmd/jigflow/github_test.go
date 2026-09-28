package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
)

// githubPlaybook is trackerPlaybook with its Tickets kept in the fake
// GitHub's acme/shop, labelled ticket, through the GitHub Issues Connector.
func githubPlaybook(t *testing.T) (*clitest.Project, *fakegithub.Server) {
	t.Helper()
	gh := fakegithub.New(t, 41)
	t.Setenv("GH_TOKEN", fakegithub.Token)
	t.Setenv("GITHUB_API_URL", gh.URL)
	p := trackerPlaybook(t)
	p.Write(".jigflow/playbook.yaml", strings.Replace(p.Read(".jigflow/playbook.yaml"),
		"    command: "+fakeConnector+"\n    settings: {tracker: tracker.json, log: calls.jsonl}\n",
		"    command: "+githubConnector+"\n    settings: {repo: acme/shop, label: ticket, create_labels: true}\n", 1))
	return p, gh
}

func TestTicketsKeptInGitHubIssuesGoThroughTheirWorkflow(t *testing.T) {
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})

	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--status", "ready-for-agent", "--field", "category=enhancement", "--link", "blocked_by=T-41")
	if i := gh.Issue(t, 42); fmt.Sprint(i.Labels) != "[ready-for-agent kind: feature ticket]" || fmt.Sprint(i.BlockedBy) != "[41]" {
		t.Errorf("issue 42 is %+v, want it labelled ready-for-agent, kind: feature and ticket, blocked by 41", i)
	}
	if first, all := agentNext(t, p); !strings.Contains(first, "T-41") || !strings.Contains(all, `T-42: not ready: waiting until every "blocked_by" item is done`) {
		t.Errorf("next should offer T-41 and hold T-42 back until T-41 is done:\n%s", all)
	}

	agentMove(t, p, "T-41", "in-progress", 0)
	if i := gh.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket status: doing]" || fmt.Sprint(i.Assignees) != "[jfl-bot]" {
		t.Errorf("issue 41 after agent A's move is %+v, want it labelled status: doing and assigned", i)
	}
	if r := p.RunInSession("B", "move", "T-41", "in-review"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "T-41 is claimed by agent session A.") {
		t.Errorf("agent B's move of A's Claim exited %d: %s", r.ExitCode, r.Stderr)
	}
	agentMove(t, p, "T-41", "in-review", 0)
	p.MustRun("move", "T-41", "done")
	if i := gh.Issue(t, 41); i.State != "closed" || len(i.Assignees) != 0 || fmt.Sprint(i.Labels) != "[ticket]" {
		t.Errorf("issue 41 when done is %+v, want it closed, unassigned, with only the ticket label", i)
	}
	if r := p.MustRun("query"); !strings.Contains(r.Stdout, `T-41 Ticket "Reset-token table": done`) || !strings.Contains(r.Stdout, `T-42 Ticket "Reset endpoint": ready-for-agent, category: enhancement`) {
		t.Errorf("query:\n%s", r.Stdout)
	}
	if first, _ := agentNext(t, p); !strings.Contains(first, "T-42") {
		t.Errorf("next once T-41 is done = %q, want T-42", first)
	}
}

func TestGitHubRateLimitingIsATrackerProblem(t *testing.T) {
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})
	gh.RateLimit()

	r := p.Run("move", "T-41", "in-progress")
	if want := `jfl move: tracker problem, not a workflow refusal: Connector "tracker" was rate-limited by the tracker: API rate limit exceeded for user ID 1. Retry in 42s.`; r.ExitCode != 3 || !strings.HasPrefix(r.Stderr, want) {
		t.Errorf("move exited %d with %q, want 3 and %q", r.ExitCode, r.Stderr, want)
	}
}
