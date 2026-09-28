package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// pocockPlaybook is the prototype's simplified Pocock Playbook. A Spec is
// broken into Tickets that are part_of it; a Ticket waits for its blocked_by
// Tickets, goes through a tests Gate (which passes once tests-pass exists)
// and a lint Gate, and is merged by a Human Transition whose Action records
// itself in ran.log. An Issue starts in needs-triage, an Inbox, and only a
// person decides who works on it.
func pocockPlaybook(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: pocock\n")
	p.Write(".jigflow/types/1-spec.yaml", `name: Spec
prefix: S
statuses: [ready-for-agent, ticketed]
initial: [ready-for-agent]
final: [ticketed]
bindings:
  ready-for-agent: to-tickets
transitions:
  - from: ready-for-agent
    to: ticketed
    guards:
      - {kind: has-incoming, link: part_of, min: 1}
`)
	p.Write(".jigflow/types/2-ticket.yaml", `name: Ticket
prefix: T
statuses: [ready-for-agent, in-progress, in-review, ready-to-merge, done]
initial: [ready-for-agent]
final: [done]
links:
  blocked_by: Ticket
  part_of: Spec
bindings:
  ready-for-agent: implement
  in-progress: implement
  in-review: code-review
readiness:
  ready-for-agent:
    - {kind: linked-all-in, link: blocked_by, statuses: [done]}
transitions:
  - from: ready-for-agent
    to: in-progress
  - from: in-progress
    to: in-review
    gates:
      - {name: tests, cmd: test -f tests-pass}
  - from: in-review
    to: in-progress
  - from: in-review
    to: ready-to-merge
    gates:
      - {name: lint, cmd: "true"}
  - from: ready-to-merge
    to: done
    human: true
    actions:
      - {name: commit, cmd: sh record.sh "merge $JFL_ARTIFACT"}
`)
	p.Write(".jigflow/types/3-issue.yaml", `name: Issue
prefix: I
statuses: [needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix, done]
initial: [needs-triage, ready-for-agent]
final: [wontfix, done]
inbox: [needs-triage]
bindings:
  needs-triage: triage
  ready-for-agent: implement
transitions:
  - {from: needs-triage, to: needs-info}
  - {from: needs-info, to: needs-triage}
  - {from: needs-triage, to: ready-for-agent, human: true}
  - {from: needs-triage, to: ready-for-human, human: true}
  - {from: needs-triage, to: wontfix, human: true}
  - {from: ready-for-agent, to: done}
  - {from: ready-for-human, to: done, human: true}
`)
	p.Write("record.sh", record)
	return p
}

// assertNoArtifact fails if the Artifact with the given id was written.
func assertNoArtifact(t *testing.T, p *clitest.Project, id string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(p.Dir, ".jigflow/state", id+".md")); err == nil {
		t.Errorf("%s was written:\n%s", id, p.Read(".jigflow/state/"+id+".md"))
	}
}

func TestAnAgentCreatingIntoAStatusThatHasABindingIsRefused(t *testing.T) {
	p := pocockPlaybook(t)

	r := p.RunInSession("A", "create", "Ticket", "--title", "Sneaky extra work")
	if r.ExitCode != 1 {
		t.Fatalf("an agent creating into ready-for-agent exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	want := `Creating a Ticket straight into "ready-for-agent" would hand it to an agent immediately. An agent must put it in a Proposal for a human to approve.`
	if !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	assertNoArtifact(t, p, "T-1")
}

func TestAnAgentMayCreateIntoAnInbox(t *testing.T) {
	p := pocockPlaybook(t)

	r := p.RunInSession("B", "create", "Issue", "--title", "Token not invalidated after use")
	if r.ExitCode != 0 {
		t.Fatalf("an agent filing into the needs-triage Inbox exited %d, want 0; stderr: %s", r.ExitCode, r.Stderr)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/I-1.md"))["status"]; got != "needs-triage" {
		t.Errorf("status = %q, want needs-triage", got)
	}

	r = p.RunInSession("B", "create", "Issue", "--title", "Fix it now", "--status", "ready-for-agent")
	if r.ExitCode != 1 {
		t.Fatalf("an agent creating an Issue into ready-for-agent, not an Inbox, exited %d, want 1", r.ExitCode)
	}
	assertNoArtifact(t, p, "I-2")
}

func TestAPersonMayCreateIntoAStatusThatHasABinding(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "ready-for-agent" {
		t.Errorf("status = %q, want ready-for-agent", got)
	}
}

// breakdown is the prototype's ticket breakdown of S-1 as a Proposal: three
// Tickets, two of them blocked_by the first through its ref, and the Spec's
// Transition to ticketed.
const breakdown = `summary: break S-1 into 3 tickets
items:
  - create: Ticket
    ref: token-table
    title: Reset-token table
    links: {part_of: [S-1]}
  - create: Ticket
    title: Reset endpoint
    links: {part_of: [S-1], blocked_by: [token-table]}
  - create: Ticket
    title: Email template
    links: {part_of: [S-1], blocked_by: [token-table]}
  - move: S-1
    to: ticketed
`

func TestAnAgentProposesASetOfCreationsAndTransitionsThatWaitsForAHuman(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)

	r := p.RunInSession("A", "propose", "breakdown.yaml")
	if r.ExitCode != 0 {
		t.Fatalf("propose exited %d, want 0; stderr: %s", r.ExitCode, r.Stderr)
	}
	if want := "proposed P-1: break S-1 into 3 tickets (4 changes, waiting for a human)"; firstLine(r.Stdout) != want {
		t.Errorf("propose said %q, want %q", firstLine(r.Stdout), want)
	}
	assertNoArtifact(t, p, "T-1")
	if got := frontmatter(t, p.Read(".jigflow/state/S-1.md"))["status"]; got != "ready-for-agent" {
		t.Errorf("S-1 status = %q: a pending Proposal changed it", got)
	}
}

func TestNextSkipsArtifactsTouchedByAPendingProposal(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	p.MustRun("propose", "breakdown.yaml")

	r := p.RunInSession("A", "next")
	if first := firstLine(r.Stdout); first != "nothing for an agent to do" {
		t.Errorf("next = %q, want nothing: S-1 waits for the breakdown's approval", first)
	}
	if want := "S-1: waiting on a pending Proposal (P-1)"; !strings.Contains(r.Stdout, want) {
		t.Errorf("next output should contain %q:\n%s", want, r.Stdout)
	}
}

// approveInTerminal approves the Proposal as a person answering y in a
// terminal, and fails the test unless it applies.
func approveInTerminal(t *testing.T, p *clitest.Project, id string) clitest.TerminalResult {
	t.Helper()
	term := p.StartInTerminal("approve", id)
	term.Expect("[y/N] ")
	term.Type("y\n")
	r := term.Wait()
	if r.ExitCode != 0 {
		t.Fatalf("approving %s exited %d, want 0; terminal:\n%s", id, r.ExitCode, r.Output)
	}
	return r
}

func TestAPersonApprovingAProposalAppliesEveryItemAndResolvesLinksWithinIt(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	p.RunInSession("A", "propose", "breakdown.yaml")

	term := p.StartInTerminal("approve", "P-1")
	term.Expect("P-1 from agent session A: break S-1 into 3 tickets")
	term.Expect(`4. move S-1 → ticketed`)
	term.Expect("Approve all 4 changes as one unit? [y/N] ")
	term.Type("y\n")
	r := term.Wait()
	if r.ExitCode != 0 {
		t.Fatalf("approve exited %d, want 0; terminal:\n%s", r.ExitCode, r.Output)
	}
	for _, want := range []string{
		"approved P-1 as one unit",
		`created T-1 "Reset-token table" in ready-for-agent`,
		`created T-3 "Email template" in ready-for-agent`,
		"S-1: ready-for-agent → ticketed",
	} {
		if !strings.Contains(r.Output, want) {
			t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
		}
	}
	if got := links(t, p.Read(".jigflow/state/T-2.md")); strings.Join(got["blocked_by"], ",") != "T-1" || strings.Join(got["part_of"], ",") != "S-1" {
		t.Errorf("T-2 links = %v, want blocked_by [T-1] (its ref resolved) and part_of [S-1]", got)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/S-1.md"))["status"]; got != "ticketed" {
		t.Errorf("S-1 status = %q, want ticketed", got)
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: approved") {
		t.Errorf("P-1 should be recorded as approved:\n%s", got)
	}
}

// proposedBreakdown is a project with the Spec S-1 and agent session A's
// pending breakdown of it, P-1.
func proposedBreakdown(t *testing.T) *clitest.Project {
	t.Helper()
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", breakdown)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	return p
}

// assertNothingApplied fails if any item of the breakdown was applied.
func assertNothingApplied(t *testing.T, p *clitest.Project) {
	t.Helper()
	assertNoArtifact(t, p, "T-1")
	if got := frontmatter(t, p.Read(".jigflow/state/S-1.md"))["status"]; got != "ready-for-agent" {
		t.Errorf("S-1 status = %q, want ready-for-agent: nothing should be applied", got)
	}
}

func TestOnlyAHumanCanApproveOrRejectAProposal(t *testing.T) {
	p := proposedBreakdown(t)

	for _, cmd := range []string{"approve", "reject"} {
		r := p.RunInSession("A", cmd, "P-1")
		if r.ExitCode != 1 {
			t.Fatalf("an agent's %s exited %d, want 1", cmd, r.ExitCode)
		}
		if want := "Only a human can " + cmd + " P-1."; !strings.Contains(r.Stderr, want) {
			t.Errorf("refusal %q should contain %q", r.Stderr, want)
		}
		tr := p.StartInTerminalInSession("A", cmd, "P-1").Wait()
		if tr.ExitCode != 1 || strings.Contains(tr.Output, "[y/N]") {
			t.Errorf("an agent's %s in a terminal exited %d, want 1 without being asked; terminal:\n%s", cmd, tr.ExitCode, tr.Output)
		}
	}
	assertNothingApplied(t, p)
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}

func TestApprovingWithoutAnInteractiveTerminalIsRefused(t *testing.T) {
	p := proposedBreakdown(t)

	r := p.Run("approve", "P-1")
	if r.ExitCode != 1 {
		t.Fatalf("approve without a TTY exited %d, want 1", r.ExitCode)
	}
	if want := "approving P-1 needs confirming in an interactive terminal"; !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	assertNothingApplied(t, p)
}

func TestAProposalIsNotAppliedUnlessTheApprovalIsConfirmed(t *testing.T) {
	p := proposedBreakdown(t)

	term := p.StartInTerminal("approve", "P-1")
	term.Expect("[y/N] ")
	term.Type("n\n")
	r := term.Wait()
	if r.ExitCode != 1 || !strings.Contains(r.Output, "P-1: not approved") {
		t.Errorf("an unconfirmed approval exited %d, want 1 saying it wasn't approved; terminal:\n%s", r.ExitCode, r.Output)
	}
	assertNothingApplied(t, p)
}

func TestAPersonRejectingAProposalChangesNothingAndReleasesItsArtifacts(t *testing.T) {
	p := proposedBreakdown(t)

	r := p.MustRun("reject", "P-1")
	if want := "rejected P-1. Nothing changed."; firstLine(r.Stdout) != want {
		t.Errorf("reject said %q, want %q", firstLine(r.Stdout), want)
	}
	assertNothingApplied(t, p)
	if first := firstLine(p.RunInSession("A", "next").Stdout); first != `run /to-tickets on S-1 "Password reset by email"` {
		t.Errorf("after the rejection, next = %q, want S-1 again", first)
	}
	if r := p.StartInTerminal("approve", "P-1").Wait(); r.ExitCode != 1 || !strings.Contains(r.Output, "P-1 isn't pending") {
		t.Errorf("approving a rejected Proposal exited %d, want 1 saying it isn't pending; terminal:\n%s", r.ExitCode, r.Output)
	}
}

func TestAnApprovalThatCannotApplyEveryItemAppliesNoneAndSaysWhichItemFailed(t *testing.T) {
	p := proposedBreakdown(t)
	// While P-1 waits, a person tickets S-1 another way, so its last item no
	// longer applies.
	p.MustRun("create", "Ticket", "--title", "Done by hand", "--link", "part_of=S-1")
	p.MustRun("move", "S-1", "ticketed")

	r := p.StartInTerminal("approve", "P-1").Wait()
	if r.ExitCode != 1 {
		t.Fatalf("an approval that can't apply exited %d, want 1; terminal:\n%s", r.ExitCode, r.Output)
	}
	if strings.Contains(r.Output, "[y/N]") {
		t.Errorf("the person was asked to approve a Proposal that can't apply:\n%s", r.Output)
	}
	want := `P-1 was not applied at all (all or nothing): item 4 (move S-1 → ticketed): S-1: "ticketed" → "ticketed" is not a declared Transition.`
	if !strings.Contains(r.Output, want) {
		t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
	}
	assertNoArtifact(t, p, "T-2")
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}

func TestAFailingGateInAProposalAppliesNoneOfIt(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-1", "in-progress")
	p.Write("proposal.yaml", `summary: send T-1 to review with a follow-up
items:
  - {create: Ticket, title: Follow-up, links: {blocked_by: [T-1]}}
  - {move: T-1, to: in-review}
`)
	p.MustRun("propose", "proposal.yaml")

	term := p.StartInTerminal("approve", "P-1")
	term.Expect("[y/N] ")
	term.Type("y\n")
	r := term.Wait()
	if r.ExitCode != 1 {
		t.Fatalf("an approval with a failing Gate exited %d, want 1; terminal:\n%s", r.ExitCode, r.Output)
	}
	want := `P-1 was not applied at all (all or nothing): item 2 (move T-1 → in-review): Gate "tests" failed`
	if !strings.Contains(r.Output, want) {
		t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
	}
	assertNoArtifact(t, p, "T-2")
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "in-progress" {
		t.Errorf("T-1 status = %q, want in-progress", got)
	}
}

func TestAProposalThatCouldNotApplyIsNotProposed(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("proposal.yaml", `summary: file a task
items:
  - {create: Ticket, title: Reset endpoint, links: {blocked_by: [token-table]}}
`)

	r := p.RunInSession("A", "propose", "proposal.yaml")
	if r.ExitCode != 1 {
		t.Fatalf("propose of an item that can't apply exited %d, want 1", r.ExitCode)
	}
	want := `not proposed: item 1 (create Ticket "Reset endpoint", blocked_by token-table): Link "blocked_by" points to token-table, which doesn't exist`
	if !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".jigflow/proposals")); err == nil {
		t.Error("a refused Proposal was recorded")
	}
}

// agentNext runs next as agent session A and returns its first line.
func agentNext(t *testing.T, p *clitest.Project) (first, all string) {
	t.Helper()
	r := p.RunInSession("A", "next")
	if r.ExitCode != 0 {
		t.Fatalf("next exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	return firstLine(r.Stdout), r.Stdout
}

// agentMove moves an Artifact as agent session A and fails the test unless it
// exits with want.
func agentMove(t *testing.T, p *clitest.Project, id, to string, want int) clitest.Result {
	t.Helper()
	r := p.RunInSession("A", "move", id, to)
	if r.ExitCode != want {
		t.Fatalf("agent A's move %s %s exited %d, want %d\nstdout: %s\nstderr: %s", id, to, r.ExitCode, want, r.Stdout, r.Stderr)
	}
	return r
}

// The prototype's "Happy path" walkthrough: Spec → Proposal of tickets →
// blocked tickets → failing Gate → review → human merge → next ticket.
func TestWalkthroughHappyPath(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")

	// Agent A asks for next work.
	if first, _ := agentNext(t, p); first != `run /to-tickets on S-1 "Password reset by email"` {
		t.Fatalf("next = %q, want /to-tickets on S-1", first)
	}
	// Agent A runs /to-tickets and proposes 3 tickets.
	p.Write("breakdown.yaml", breakdown)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	// Agent A asks for next work again: nothing is agent work until you approve.
	if first, all := agentNext(t, p); first != "nothing for an agent to do" || !strings.Contains(all, "S-1: waiting on a pending Proposal") {
		t.Fatalf("next while P-1 is pending:\n%s", all)
	}
	// You approve the breakdown (one unit).
	approveInTerminal(t, p, "P-1")
	// Agent A asks for next work: T-2 and T-3 are blocked by T-1.
	first, all := agentNext(t, p)
	if first != `run /implement on T-1 "Reset-token table"` {
		t.Fatalf("next = %q, want /implement on T-1", first)
	}
	for _, id := range []string{"T-2", "T-3"} {
		if want := id + `: not ready: waiting until every "blocked_by" item is done`; !strings.Contains(all, want) {
			t.Errorf("next should say %q:\n%s", want, all)
		}
	}
	// Agent A starts T-1, and sends it to review while tests are failing.
	agentMove(t, p, "T-1", "in-progress", 0)
	if r := agentMove(t, p, "T-1", "in-review", 1); !strings.Contains(r.Stderr, `Gate "tests" failed`) {
		t.Errorf("the tests Gate should refuse T-1:\n%s", r.Stderr)
	}
	// Agent A fixes the code: tests now pass. It sends T-1 to review again.
	p.Write("tests-pass", "")
	agentMove(t, p, "T-1", "in-review", 0)
	if first, _ := agentNext(t, p); first != `run /code-review on T-1 "Reset-token table"` {
		t.Fatalf("next = %q, want /code-review on T-1", first)
	}
	// Agent A finishes /code-review: T-1 is ready to merge, which is human work.
	agentMove(t, p, "T-1", "ready-to-merge", 0)
	if first, all := agentNext(t, p); first != "nothing for an agent to do" || !strings.Contains(all, `T-1: "ready-to-merge" has no Binding, so it's human work`) {
		t.Fatalf("next with T-1 waiting for the merge:\n%s", all)
	}
	// You merge T-1.
	term := p.StartInTerminal("move", "T-1", "done")
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("merging T-1 exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}
	if got := p.Read("ran.log"); got != "merge T-1\n" {
		t.Errorf("ran.log = %q, want the merge Action for T-1", got)
	}
	// Agent A asks for next work: the next ticket is unblocked.
	if first, _ := agentNext(t, p); first != `run /implement on T-2 "Reset endpoint"` {
		t.Errorf("next = %q, want /implement on T-2", first)
	}
}

// The prototype's "Agent tries to skip you" walkthrough: with T-1 waiting in
// your merge queue, the agent tries creating agent-ready work directly and
// merging on its own. Both are refused; it proposes the merge, and you decide.
func TestWalkthroughAgentTriesToSkipYou(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("tests-pass", "")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	for _, to := range []string{"in-progress", "in-review", "ready-to-merge"} {
		agentMove(t, p, "T-1", to, 0)
	}

	// Agent A creates a ticket directly in ready-for-agent.
	r := p.RunInSession("A", "create", "Ticket", "--title", "Sneaky extra work", "--status", "ready-for-agent")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "An agent must put it in a Proposal for a human to approve.") {
		t.Errorf("creating agent-ready work directly exited %d, want 1 with a refusal; stderr: %s", r.ExitCode, r.Stderr)
	}
	assertNoArtifact(t, p, "T-2")
	// Agent A tries to merge T-1 itself.
	if r := agentMove(t, p, "T-1", "done", 1); !strings.Contains(r.Stderr, "is a Human Transition. An agent can only propose it.") {
		t.Errorf("merging on its own should be refused as a Human Transition:\n%s", r.Stderr)
	}
	// Agent A proposes merging T-1 instead.
	p.Write("merge.yaml", "summary: merge T-1\nitems:\n  - {move: T-1, to: done}\n")
	if r := p.RunInSession("A", "propose", "merge.yaml"); r.ExitCode != 0 || firstLine(r.Stdout) != "proposed P-1: merge T-1 (1 change, waiting for a human)" {
		t.Fatalf("propose exited %d, said %q; stderr: %s", r.ExitCode, firstLine(r.Stdout), r.Stderr)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "ready-to-merge" {
		t.Fatalf("T-1 status = %q before approval, want ready-to-merge", got)
	}
	// You approve the merge: the Human Transition happens inside the approved
	// Proposal, and its Action runs.
	out := approveInTerminal(t, p, "P-1").Output
	if !strings.Contains(out, "T-1: ready-to-merge → done") {
		t.Errorf("approval should report the merge; terminal:\n%s", out)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "done" {
		t.Errorf("T-1 status = %q, want done", got)
	}
	if got := p.Read("ran.log"); got != "merge T-1\n" {
		t.Errorf("ran.log = %q, want the merge Action for T-1", got)
	}
}

func TestAProposalMayMoveAnArtifactItCreates(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("proposal.yaml", `summary: file a bug and hand it to an agent
items:
  - {create: Issue, ref: bug, title: Token not invalidated after use}
  - {move: bug, to: ready-for-agent}
`)
	p.MustRun("propose", "proposal.yaml")

	approveInTerminal(t, p, "P-1")
	if got := frontmatter(t, p.Read(".jigflow/state/I-1.md"))["status"]; got != "ready-for-agent" {
		t.Errorf("I-1 status = %q, want ready-for-agent", got)
	}
}

func TestApprovalRefusesAProposalThatMovesAnArtifactWhoseFrontmatterWasChangedOutsideJfl(t *testing.T) {
	p := proposedBreakdown(t)
	p.Write(".jigflow/state/S-1.md", strings.Replace(p.Read(".jigflow/state/S-1.md"), "title: Password reset by email", "title: Something else", 1))

	r := p.StartInTerminal("approve", "P-1").Wait()
	if r.ExitCode != 1 {
		t.Fatalf("approve exited %d, want 1; terminal:\n%s", r.ExitCode, r.Output)
	}
	if want := "item 4 (move S-1 → ticketed): S-1: its frontmatter was changed outside jfl"; !strings.Contains(r.Output, want) {
		t.Errorf("terminal should say %q; it showed:\n%s", want, r.Output)
	}
	assertNoArtifact(t, p, "T-1")
}
