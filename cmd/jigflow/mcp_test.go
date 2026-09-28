package main_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

func TestTheMCPServerListsOnlyTheAgentSafeTools(t *testing.T) {
	p := pocockPlaybook(t)
	m := p.StartMCP("A")

	tools := m.Tools()
	slices.Sort(tools)
	if want := []string{"create", "move", "next", "propose", "query"}; !slices.Equal(tools, want) {
		t.Errorf("tools/list = %v, want %v", tools, want)
	}
}

func TestTheMCPServerCannotApproveOrRejectAProposal(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	m := p.StartMCP("A")

	for _, tool := range []string{"approve", "reject"} {
		_, err := m.CallTool(tool, map[string]any{"proposal": "P-1"})
		var rerr *clitest.RPCError
		if !errors.As(err, &rerr) || rerr.Code != -32602 || !strings.Contains(rerr.Message, fmt.Sprintf("unknown tool %q", tool)) {
			t.Errorf("tools/call %s = %v, want an unknown-tool error", tool, err)
		}
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 after the MCP calls =\n%s\nwant it still pending", got)
	}
}

func TestTheMCPServerMakesAnAgentTransitionAndClaimsTheArtifactForItsSession(t *testing.T) {
	p := shipPlaybook(t)
	p.Write("pass.flag", "")
	m := p.StartMCP("A")

	r := m.MustCallTool("move", map[string]any{"id": "T-1", "status": "built"})
	if r.IsError || r.Text != "T-1: open → built\n" {
		t.Errorf("move = %+v, want T-1 moved to built", r)
	}
	if fm := frontmatter(t, p.Read(".jigflow/state/T-1.md")); fm["status"] != "built" || fm["claim"] != "A" {
		t.Errorf("T-1 frontmatter = %v, want built and claimed by A", fm)
	}
}

func TestTheMCPServerRefusesAHumanTransitionAsTheCLIDoes(t *testing.T) {
	p := shipPlaybook(t)
	p.Write("pass.flag", "")
	agentMove(t, p, "T-1", "built", 0)
	cli := agentMove(t, p, "T-1", "done", 1)
	m := p.StartMCP("A")

	r := m.MustCallTool("move", map[string]any{"id": "T-1", "status": "done"})
	if !r.IsError || r.Text != cli.Stderr {
		t.Errorf("move = %+v, want an error with the CLI's refusal %q", r, cli.Stderr)
	}
	if !strings.Contains(r.Text, `"built" → "done" is a Human Transition. An agent can only propose it.`) {
		t.Errorf("refusal = %q, want it to say it is a Human Transition", r.Text)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "built" {
		t.Errorf("T-1 status = %q, want it still built", got)
	}
}

func TestTheMCPServerHandsOutNextAndProposesForAPersonToApprove(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	m := p.StartMCP("A")

	if r := m.MustCallTool("next", map[string]any{}); r.IsError || firstLine(r.Text) != `run /to-tickets on S-1 "Password reset by email"` {
		t.Errorf("next = %+v, want /to-tickets on S-1", r)
	}
	if focus := focusOf(t, p, "A"); focus != "S-1" {
		t.Errorf("session A's Focus = %q, want S-1", focus)
	}
	r := m.MustCallTool("propose", map[string]any{"file": "breakdown.yaml"})
	if want := "proposed P-1: break S-1 into 3 tickets (4 changes, waiting for a human)"; r.IsError || firstLine(r.Text) != want {
		t.Errorf("propose = %+v, want %q", r, want)
	}
	assertNoArtifact(t, p, "T-1")
	if r := m.MustCallTool("next", map[string]any{"autopilot": true}); firstLine(r.Text) != "autopilot stopped: nothing for an agent to do" {
		t.Errorf("an autopilot step = %+v, want it stopped by the pending Proposal", r)
	}
}

func TestTheMCPServerIsAnAgentSessionEvenWithoutJFLSession(t *testing.T) {
	p := shipPlaybook(t)
	cli := p.RunInSession("A", "create", "Ticket", "--title", "Sneaky extra work")
	if cli.ExitCode != 1 {
		t.Fatalf("an agent creating into open exited %d, want 1", cli.ExitCode)
	}
	m := p.StartMCP("")

	r := m.MustCallTool("create", map[string]any{"type": "Ticket", "title": "Sneaky extra work"})
	if !r.IsError || r.Text != cli.Stderr {
		t.Errorf("create = %+v, want the CLI's refusal to an agent %q", r, cli.Stderr)
	}
	p.Write("pass.flag", "")
	m.MustCallTool("move", map[string]any{"id": "T-1", "status": "built"})
	claim := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["claim"]
	if !strings.HasPrefix(claim, "mcp-") {
		t.Errorf("T-1 claim = %q, want a session id the server made up", claim)
	}
}

func TestTheMCPServerFilesAnArtifactIntoAnInbox(t *testing.T) {
	p := pocockPlaybook(t)
	m := p.StartMCP("B")

	r := m.MustCallTool("create", map[string]any{"type": "Issue", "title": "Token not invalidated after use"})
	if r.IsError || r.Text != "created I-1 \"Token not invalidated after use\" in needs-triage\n" {
		t.Errorf("create = %+v, want I-1 filed into needs-triage", r)
	}
	cli := p.RunInSession("B", "create", "Issue", "--title", "Skip triage", "--status", "ready-for-agent")
	r = m.MustCallTool("create", map[string]any{"type": "Issue", "title": "Skip triage", "status": "ready-for-agent"})
	if !r.IsError || r.Text != cli.Stderr {
		t.Errorf("create into ready-for-agent = %+v, want the CLI's refusal %q", r, cli.Stderr)
	}
	cli = p.RunInSession("B", "create", "Issue", "--title", "Linked", "--link", "part_of=S-1")
	r = m.MustCallTool("create", map[string]any{"type": "Issue", "title": "Linked", "links": map[string]any{"part_of": []string{"S-1"}}})
	if !r.IsError || r.Text != cli.Stderr || !strings.Contains(r.Text, `has no Link "part_of"`) {
		t.Errorf("create with an undeclared Link = %+v, want the CLI's refusal %q", r, cli.Stderr)
	}
	assertNoArtifact(t, p, "I-2")
}

func TestQueryListsTheArtifactsOfATypeOrInAStatus(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.MustRun("create", "Issue", "--title", "Token not invalidated after use")
	p.MustRun("create", "Issue", "--title", "Typo on login", "--status", "ready-for-agent")
	agentMove(t, p, "I-2", "done", 0)
	p.MustRun("create", "Ticket", "--title", "Reset-token table", "--link", "part_of=S-1")
	agentMove(t, p, "T-1", "in-progress", 0)

	if r := p.MustRun("query"); r.Stdout != `I-1 Issue "Token not invalidated after use": needs-triage
I-2 Issue "Typo on login": done
S-1 Spec "Password reset by email": ready-for-agent
T-1 Ticket "Reset-token table": in-progress, claimed by agent session A, part_of: S-1
` {
		t.Errorf("query =\n%s", r.Stdout)
	}
	if r := p.MustRun("query", "--type", "Issue", "--status", "needs-triage"); r.Stdout != "I-1 Issue \"Token not invalidated after use\": needs-triage\n" {
		t.Errorf("query of Issues in needs-triage =\n%s", r.Stdout)
	}
	if r := p.MustRun("query", "--status", "in-review"); r.Stdout != "no Artifacts\n" {
		t.Errorf("query of an empty Status =\n%s", r.Stdout)
	}
	if r := p.Run("query", "--type", "Bug"); r.ExitCode != 1 || !strings.Contains(r.Stderr, `unknown Artifact Type "Bug". Declared Types: Spec, Ticket, Issue`) {
		t.Errorf("query of an unknown Type: exit %d, stderr %q; want it refused", r.ExitCode, r.Stderr)
	}
}

func TestTheMCPServerQueriesAsTheCLIDoes(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.MustRun("create", "Issue", "--title", "Token not invalidated after use")
	m := p.StartMCP("A")

	cli := p.MustRun("query", "--type", "Issue")
	if r := m.MustCallTool("query", map[string]any{"type": "Issue"}); r.IsError || r.Text != cli.Stdout {
		t.Errorf("query = %+v, want the CLI's %q", r, cli.Stdout)
	}
	cli = p.Run("query", "--status", "needs-triage", "--type", "Bug")
	if r := m.MustCallTool("query", map[string]any{"type": "Bug", "status": "needs-triage"}); !r.IsError || r.Text != cli.Stderr {
		t.Errorf("query of an unknown Type = %+v, want the CLI's refusal %q", r, cli.Stderr)
	}
}

func TestJflMCPServesMCPOverStdio(t *testing.T) {
	p := ticketPlaybook(t)
	m := p.StartMCP("A")

	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := m.Call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}, &init); err != nil {
		t.Fatal(err)
	}
	if _, ok := init.Capabilities["tools"]; init.ProtocolVersion != "2025-06-18" || !ok || init.ServerInfo.Name != "jigflow" {
		t.Errorf("initialize = %+v, want protocol 2025-06-18, the tools capability and serverInfo jigflow", init)
	}
	if err := m.Call("ping", map[string]any{}, nil); err != nil {
		t.Errorf("ping: %v", err)
	}
	var rerr *clitest.RPCError
	if err := m.Call("resources/list", map[string]any{}, nil); !errors.As(err, &rerr) || rerr.Code != -32601 {
		t.Errorf("an unknown method = %v, want a method-not-found error", err)
	}
}
