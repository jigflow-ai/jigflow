package main_test

import (
	"errors"
	"fmt"
	"regexp"
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
	if want := []string{"create", "move", "next", "propose", "query", "show"}; !slices.Equal(tools, want) {
		t.Errorf("tools/list = %v, want %v", tools, want)
	}
}

func TestTheMCPServerCannotApproveOrRejectAProposalForAClientThatCannotShowAForm(t *testing.T) {
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
	cli = p.MustRun("show", "I-1")
	if r := m.MustCallTool("show", map[string]any{"id": "I-1"}); r.IsError || r.Text != cli.Stdout {
		t.Errorf("show = %+v, want the CLI's %q", r, cli.Stdout)
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

func TestJflMCPNegotiates2025_11_25WhereTheClientOffersItAnd2025_06_18Otherwise(t *testing.T) {
	p := ticketPlaybook(t)

	for offered, want := range map[string]string{
		"2025-11-25": "2025-11-25",
		"2025-06-18": "2025-06-18",
		"2025-03-26": "2025-06-18",
	} {
		if got := p.StartMCPWith("A", clitest.MCPClient{Protocol: offered}).Protocol; got != want {
			t.Errorf("a client offering %s got protocol %s, want %s", offered, got, want)
		}
	}
}

func TestTheMCPServerOffersApproveOnlyToAClientThatCanShowThePersonAForm(t *testing.T) {
	p := pocockPlaybook(t)

	for _, c := range []clitest.MCPClient{{Protocol: "2025-06-18", Elicitation: true}, {Protocol: "2025-11-25", Elicitation: true}} {
		tools := p.StartMCPWith("A", c).Tools()
		slices.Sort(tools)
		if want := []string{"approve", "create", "move", "next", "propose", "query", "show"}; !slices.Equal(tools, want) {
			t.Errorf("tools/list to a %s client with elicitation = %v, want %v", c.Protocol, tools, want)
		}
	}
	if tools := p.StartMCPWith("A", clitest.MCPClient{Protocol: "2025-11-25"}).Tools(); slices.Contains(tools, "approve") {
		t.Errorf("tools/list to a client without elicitation = %v, want no approve", tools)
	}
}

// eliciting is a scripted MCP client that can show the person a form.
var eliciting = clitest.MCPClient{Protocol: "2025-11-25", Elicitation: true}

// breakdownForm is the form jfl writes to ask for a Confirmation on
// proposedBreakdown's P-1.
const breakdownForm = `P-1 from agent session A: break S-1 into 3 tickets
  1. create Ticket "Reset-token table" in ready-for-agent, part_of S-1
  2. create Ticket "Reset endpoint" in ready-for-agent, blocked_by token-table, part_of S-1
  3. create Ticket "Email template" in ready-for-agent, blocked_by token-table, part_of S-1
  4. move S-1 → ticketed
Approve all 4 changes as one unit, or reject P-1?`

func TestAPersonApprovesAPendingProposalInAFormTheAgentsClientShowsThem(t *testing.T) {
	p := proposedBreakdown(t)
	m := p.StartMCPWith("A", eliciting)
	m.Answer(clitest.Accept("approve"))

	r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"})
	if r.IsError || firstLine(r.Text) != "approved P-1 as one unit:" || !strings.Contains(r.Text, `created T-2 "Reset endpoint" in ready-for-agent`) {
		t.Errorf("approve = %+v, want P-1 approved and what jfl printed", r)
	}
	forms := m.Forms()
	if len(forms) != 1 {
		t.Fatalf("the client was asked for %d forms, want 1", len(forms))
	}
	if forms[0].Message != breakdownForm {
		t.Errorf("the form's message =\n%s\nwant\n%s", forms[0].Message, breakdownForm)
	}
	if forms[0].Mode != "form" {
		t.Errorf("the form's mode = %q, want form", forms[0].Mode)
	}
	if got := links(t, p.Read(".jigflow/state/T-2.md")); strings.Join(got["blocked_by"], ",") != "T-1" {
		t.Errorf("T-2 links = %v, want blocked_by [T-1]", got)
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: approved") {
		t.Errorf("P-1 should be recorded as approved:\n%s", got)
	}
	if got := entriesVia(t, p, "ticketed"); got["agent"] != 1 || len(got) != 1 {
		t.Errorf("S-1's move into ticketed was recorded via %v, want agent", got)
	}
	if l := p.MustRun("ledger").Stdout; !regexp.MustCompile(`S-1\s+ready-for-agent → ticketed\s+via agent`).MatchString(l) {
		t.Errorf("jfl ledger should show S-1's move confirmed via agent:\n%s", l)
	}
}

func TestTheFormAsksForOneRequiredDecisionToApproveOrReject(t *testing.T) {
	p := proposedBreakdown(t)
	m := p.StartMCPWith("A", clitest.MCPClient{Protocol: "2025-06-18", Elicitation: true})
	m.Answer(clitest.Cancel)

	m.MustCallTool("approve", map[string]any{"proposal": "P-1"})
	forms := m.Forms()
	if len(forms) != 1 {
		t.Fatalf("the client was asked for %d forms, want 1", len(forms))
	}
	s := forms[0].Schema
	props, _ := s["properties"].(map[string]any)
	decision, _ := props["decision"].(map[string]any)
	if s["type"] != "object" || len(props) != 1 || decision["type"] != "string" || fmt.Sprint(decision["enum"]) != "[approve reject]" || fmt.Sprint(s["required"]) != "[decision]" {
		t.Errorf("the form's schema = %v, want one required string decision: approve or reject", s)
	}
	if forms[0].Mode != "" {
		t.Errorf("the form's mode = %q, want none under 2025-06-18", forms[0].Mode)
	}
}

func TestAPersonRejectingInTheFormDropsTheProposal(t *testing.T) {
	p := proposedBreakdown(t)
	m := p.StartMCPWith("A", eliciting)
	m.Answer(clitest.Accept("reject"))

	r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"})
	if r.IsError || r.Text != "rejected P-1. Nothing changed.\n" {
		t.Errorf("approve answered with reject = %+v, want P-1 rejected", r)
	}
	assertNothingApplied(t, p)
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: rejected") {
		t.Errorf("P-1 should be recorded as rejected:\n%s", got)
	}
}

func TestAFormNotAnsweredLeavesTheProposalPending(t *testing.T) {
	for name, c := range map[string]struct {
		answer clitest.FormAnswer
		why    string
	}{
		"declined":     {clitest.Decline, "the form was declined"},
		"dismissed":    {clitest.Cancel, "the form was dismissed"},
		"client error": {clitest.FailForm, "the client couldn't ask the person (the client couldn't show the form)"},
		"no choice":    {clitest.AcceptWith(map[string]any{}), `the form was submitted with decision "", not one of approve, reject`},
		"not a choice": {clitest.AcceptWith(map[string]any{"decision": 7}), "the client's answer to the form doesn't decode"},
	} {
		t.Run(name, func(t *testing.T) {
			p := proposedBreakdown(t)
			m := p.StartMCPWith("A", eliciting)
			m.Answer(c.answer)

			r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"})
			if want := "jfl approve: P-1: not approved, still pending: " + c.why + "\n"; !r.IsError || r.Text != want {
				t.Errorf("approve = %+v, want an error %q", r, want)
			}
			assertNothingApplied(t, p)
			if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
				t.Errorf("P-1 should still be pending:\n%s", got)
			}
			if first, _ := agentNext(t, p); strings.Contains(first, "S-1") {
				t.Errorf("next = %q, want S-1 still skipped while P-1 waits", first)
			}
			// The person can still be asked later.
			m.Answer(clitest.Accept("approve"))
			if r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"}); r.IsError {
				t.Errorf("asking again = %+v, want P-1 approved", r)
			}
		})
	}
}

func TestAProposalMakingATransitionThatRequiresTheDashboardIsRefusedWithoutAForm(t *testing.T) {
	p := dashboardMergePlaybook(t)
	p.Write("merge.yaml", "summary: merge T-1\nitems:\n  - {move: T-1, to: done}\n")
	if r := p.RunInSession("A", "propose", "merge.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	m := p.StartMCPWith("A", eliciting)
	m.Answer(clitest.Accept("approve"))

	r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"})
	want := `P-1 was not applied at all (all or nothing): item 1 (move T-1 → done): T-1: "ready-to-merge" → "done" is a Human Transition the Playbook requires making in the Dashboard: run jfl ui and approve P-1 there`
	if !r.IsError || !strings.Contains(r.Text, want) {
		t.Errorf("approve = %+v, want the Dashboard pointer %q", r, want)
	}
	if n := len(m.Forms()); n != 0 {
		t.Errorf("the client was asked for %d forms, want none", n)
	}
	assertNothingRan(t, p)
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}

func TestAnApprovalInTheFormRunsTheGatesAndActionsAsTheTerminalDoes(t *testing.T) {
	p := mergePlaybook(t)
	p.Write("merge.yaml", "summary: merge T-1\nitems:\n  - {move: T-1, to: done}\n")
	if r := p.RunInSession("A", "propose", "merge.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	m := p.StartMCPWith("A", eliciting)
	m.Answer(clitest.Accept("approve"))

	r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"})
	if want := "approved P-1 as one unit:\n  T-1: ready-to-merge → done\nAction \"commit\" succeeded\n"; r.IsError || r.Text != want {
		t.Errorf("approve = %+v, want %q", r, want)
	}
	if got := p.Read("ran.log"); got != "gate\naction\n" {
		t.Errorf("ran.log = %q, want the Gate, then the Action", got)
	}
	if msg := m.Forms()[0].Message; !strings.Contains(msg, "  1. move T-1 → done\n") {
		t.Errorf("the form's message =\n%s\nwant the move with its target Status", msg)
	}
}

func TestAnApprovalInTheFormReachesTheTracker(t *testing.T) {
	p := trackerPlaybook(t)
	p.Write("breakdown.yaml", `summary: file the reset flow
items:
  - {create: Ticket, ref: table, title: Reset-token table, status: ready-for-agent}
  - {move: table, to: in-progress}
`)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d: %s", r.ExitCode, r.Stderr)
	}
	m := p.StartMCPWith("A", eliciting)
	m.Answer(clitest.Accept("approve"))

	if r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"}); r.IsError || !strings.Contains(r.Text, `created T-41 "Reset-token table" in ready-for-agent`) {
		t.Errorf("approve = %+v, want T-41 created in the tracker", r)
	}
	if table := item(t, p, "41"); strings.Join(table.Labels, ",") != "status: doing" {
		t.Errorf("T-41 is %+v, want it in-progress in the tracker", table)
	}
}

func TestTheFormNamesEachPlaybookChange(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("tone.yaml", "summary: agree a tone\nitems:\n  - {guideline: tone, text: Be terse.}\n  - {gate: tests, cmd: go test ./...}\n")
	if r := p.RunInSession("A", "propose", "tone.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	m := p.StartMCPWith("A", eliciting)
	m.Answer(clitest.Cancel)

	m.MustCallTool("approve", map[string]any{"proposal": "P-1"})
	want := "P-1 from agent session A: agree a tone\n  1. add Guideline \"tone\" (1 line)\n  2. give Gate \"tests\" the command go test ./...\nApprove all 2 changes as one unit, or reject P-1?"
	if forms := m.Forms(); len(forms) != 1 || forms[0].Message != want {
		t.Errorf("forms = %+v, want one with message\n%s", forms, want)
	}
}

func TestAToolCallTheClientCancelsWhileTheFormIsOpenLeavesTheProposalPending(t *testing.T) {
	p := proposedBreakdown(t)
	m := p.StartMCPWith("A", eliciting)
	m.Answer(clitest.CancelCall)

	if _, err := m.CallTool("approve", map[string]any{"proposal": "P-1"}); !errors.Is(err, clitest.ErrCancelled) {
		t.Fatalf("approve = %v, want it cancelled", err)
	}
	cli := p.MustRun("query", "--type", "Spec")
	if r := m.MustCallTool("query", map[string]any{"type": "Spec"}); r.IsError || r.Text != cli.Stdout {
		t.Errorf("the next call = %+v, want the server answering again with %q", r, cli.Stdout)
	}
	assertNothingApplied(t, p)
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}
