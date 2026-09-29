package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"go.yaml.in/yaml/v3"
)

// claimOf returns the Claim recorded in an Artifact file, or "" for none.
func claimOf(t *testing.T, content string) string {
	t.Helper()
	fm, _, _ := strings.Cut(strings.TrimPrefix(content, "---\n"), "\n---\n")
	var v struct {
		Claim string `yaml:"claim"`
	}
	if err := yaml.Unmarshal([]byte(fm), &v); err != nil {
		t.Fatalf("frontmatter is not YAML: %v\n%s", err, fm)
	}
	return v.Claim
}

func TestAnAgentSessionsTransitionClaimsTheArtifactForThatSession(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	if got := claimOf(t, p.Read(".jigflow/state/T-1.md")); got != "" {
		t.Fatalf("a new Artifact is claimed by %q, want no Claim", got)
	}

	agentMove(t, p, "T-1", "in-progress", 0)
	if got := claimOf(t, p.Read(".jigflow/state/T-1.md")); got != "A" {
		t.Errorf("claim = %q after agent session A's Transition, want A", got)
	}
}

func TestAnotherAgentSessionsTransitionOnAClaimedArtifactIsRefused(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("tests-pass", "")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	agentMove(t, p, "T-1", "in-progress", 0)
	before := p.Read(".jigflow/state/T-1.md")

	r := p.RunInSession("B", "move", "T-1", "in-review")
	if r.ExitCode != 1 {
		t.Fatalf("agent session B's move of A's T-1 exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	if want := "T-1 is claimed by agent session A."; !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\n%s", after)
	}
}

func TestAPersonIsNotBlockedByAClaim(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("tests-pass", "")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	agentMove(t, p, "T-1", "in-progress", 0)

	p.MustRun("move", "T-1", "in-review")
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "in-review" {
		t.Errorf("status = %q, want in-review", got)
	}
	if got := claimOf(t, p.Read(".jigflow/state/T-1.md")); got != "A" {
		t.Errorf("claim = %q after a person's move into a Status with a Binding, want A's Claim kept", got)
	}
}

func TestTheClaimIsReleasedWhenTheArtifactEntersAStatusWithNoBinding(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("tests-pass", "")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	for _, to := range []string{"in-progress", "in-review"} {
		agentMove(t, p, "T-1", to, 0)
	}

	agentMove(t, p, "T-1", "ready-to-merge", 0)
	if got := claimOf(t, p.Read(".jigflow/state/T-1.md")); got != "" {
		t.Errorf("claim = %q in ready-to-merge, which has no Binding; want it released", got)
	}
}

func TestTheClaimIsReleasedWhenTheArtifactEntersAFinalStatus(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Issue", "--title", "Reset link expires instantly", "--status", "ready-for-agent")
	agentMove(t, p, "I-1", "done", 0)
	if got := claimOf(t, p.Read(".jigflow/state/I-1.md")); got != "" {
		t.Errorf("claim = %q after agent A's move into the final done, want none", got)
	}

	// A person's move into a final Status releases a session's Claim too.
	p.MustRun("create", "Issue", "--title", "Token not invalidated after use")
	p.MustRun("move", "I-2", "needs-info")
	agentMove(t, p, "I-2", "needs-triage", 0)
	if got := claimOf(t, p.Read(".jigflow/state/I-2.md")); got != "A" {
		t.Fatalf("claim = %q, want A before the person's move", got)
	}
	term := p.StartInTerminal("move", "I-2", "wontfix")
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("a person's move of I-2 to wontfix exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}
	if got := claimOf(t, p.Read(".jigflow/state/I-2.md")); got != "" {
		t.Errorf("claim = %q after a person's move into the final wontfix, want none", got)
	}
}

func TestAnApprovedProposalReleasesTheClaimsOfTheArtifactsItFinishes(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("tests-pass", "")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Email template")
	for _, to := range []string{"in-progress", "in-review", "ready-to-merge"} {
		agentMove(t, p, "T-1", to, 0)
	}
	agentMove(t, p, "T-2", "in-progress", 0)
	p.Write("proposal.yaml", `summary: merge T-1 and send T-2 back to the queue
items:
  - {move: T-1, to: done}
  - {move: T-2, to: in-review}
`)
	p.MustRun("propose", "proposal.yaml")

	approveInTerminal(t, p, "P-1")
	if got := claimOf(t, p.Read(".jigflow/state/T-1.md")); got != "" {
		t.Errorf("T-1 claim = %q after its approved merge, want none", got)
	}
	if got := claimOf(t, p.Read(".jigflow/state/T-2.md")); got != "A" {
		t.Errorf("T-2 claim = %q after an approved move into in-review, which has a Binding; want A's Claim kept", got)
	}
}

func TestNextSkipsArtifactsClaimedByAnotherSessionAndSaysWhoClaimsThem(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Email template")
	agentMove(t, p, "T-1", "in-progress", 0)

	r := p.RunInSession("B", "next")
	if first := firstLine(r.Stdout); first != `run /implement on T-2 "Email template"` {
		t.Errorf("agent B's next = %q, want T-2: T-1 is A's", first)
	}
	if want := "T-1: claimed by agent session A"; !strings.Contains(r.Stdout, want) {
		t.Errorf("next output should contain %q:\n%s", want, r.Stdout)
	}
	if first, _ := agentNext(t, p); first != `run /implement on T-1 "Reset-token table"` {
		t.Errorf("agent A's next = %q, want its own T-1", first)
	}
}

func TestNextPrefersTheArtifactTheSessionAlreadyClaims(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Email template")
	p.RunInSession("B", "move", "T-2", "in-progress")

	r := p.RunInSession("B", "next")
	if first := firstLine(r.Stdout); first != `run /implement on T-2 "Email template"` {
		t.Errorf("agent B's next = %q, want its own T-2 before the earlier T-1", first)
	}
}

// focusOf returns the Focus recorded for the agent session, or "" for none.
func focusOf(t *testing.T, p *clitest.Project, session string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.Dir, ".jigflow/sessions", session+".yaml"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Focus string `yaml:"focus"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("session file is not YAML: %v\n%s", err, data)
	}
	return v.Focus
}

func TestNextSetsTheSessionsFocusToItsPick(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Email template")
	agentMove(t, p, "T-1", "in-progress", 0)

	agentNext(t, p)
	p.RunInSession("B", "next")
	if got := focusOf(t, p, "A"); got != "T-1" {
		t.Errorf("agent A's Focus = %q, want T-1", got)
	}
	if got := focusOf(t, p, "B"); got != "T-2" {
		t.Errorf("agent B's Focus = %q, want T-2", got)
	}

	// With nothing left for it, the session has no Focus.
	p.Write("proposal.yaml", "summary: start T-2\nitems:\n  - {move: T-2, to: in-progress}\n")
	p.MustRun("propose", "proposal.yaml")
	if first := firstLine(p.RunInSession("B", "next").Stdout); first != "nothing for an agent to do" {
		t.Fatalf("agent B's next = %q, want nothing: T-1 is A's and T-2 waits on P-1", first)
	}
	if got := focusOf(t, p, "B"); got != "" {
		t.Errorf("agent B's Focus = %q with nothing to do, want none", got)
	}
	if got := focusOf(t, p, "A"); got != "T-1" {
		t.Errorf("agent A's Focus = %q, want T-1 still", got)
	}
}

func TestASessionsFocusIsNeverCommitted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	agentNext(t, p)
	if got := focusOf(t, p, "A"); got != "T-1" {
		t.Fatalf("agent A's Focus = %q, want T-1", got)
	}

	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = p.Dir
		return cmd.Run()
	}
	if err := git("init", "-q"); err != nil {
		t.Fatal(err)
	}
	if err := git("check-ignore", "-q", ".jigflow/sessions/A.yaml"); err != nil {
		t.Errorf("git would commit agent A's Focus: check-ignore said %v", err)
	}
	if err := git("check-ignore", "-q", ".jigflow/state/T-1.md"); err == nil {
		t.Error("git ignores T-1, which should be committed")
	}
}

func TestAnArtifactLeavesEverySessionsFocusWhenItStopsBeingAgentWork(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write("tests-pass", "")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Issue", "--title", "Reset link expires instantly", "--status", "ready-for-agent")
	agentMove(t, p, "T-1", "in-progress", 0)
	agentNext(t, p)
	p.RunInSession("B", "next")
	if a, b := focusOf(t, p, "A"), focusOf(t, p, "B"); a != "T-1" || b != "I-1" {
		t.Fatalf("Focus of A = %q, B = %q; want T-1 and I-1", a, b)
	}

	agentMove(t, p, "T-1", "in-review", 0)
	if got := focusOf(t, p, "A"); got != "T-1" {
		t.Errorf("agent A's Focus = %q after moving T-1 into in-review, which has a Binding; want T-1", got)
	}
	agentMove(t, p, "T-1", "ready-to-merge", 0)
	if got := focusOf(t, p, "A"); got != "" {
		t.Errorf("agent A's Focus = %q after T-1 became human work, want none", got)
	}

	// A person finishing the work takes it out of the session's Focus too.
	p.MustRun("move", "I-1", "done")
	if got := focusOf(t, p, "B"); got != "" {
		t.Errorf("agent B's Focus = %q after a person finished I-1, want none", got)
	}
}

func TestAnApprovedProposalTakesTheArtifactsItFinishesOutOfEverySessionsFocus(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Issue", "--title", "Reset link expires instantly", "--status", "ready-for-agent")
	agentNext(t, p)
	p.Write("proposal.yaml", "summary: close I-1\nitems:\n  - {move: I-1, to: done}\n")
	p.MustRun("propose", "proposal.yaml")

	approveInTerminal(t, p, "P-1")
	if got := focusOf(t, p, "A"); got != "" {
		t.Errorf("agent A's Focus = %q after the approved Proposal finished I-1, want none", got)
	}
}

// The prototype's "Two sessions in parallel" walkthrough: two agent sessions
// share one backlog. A Claim makes sure they never pick the same ticket, and
// a blocked ticket is skipped by both.
func TestWalkthroughTwoSessionsInParallel(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Email template")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "blocked_by=T-1")
	blocked := `T-3: not ready: waiting until every "blocked_by" item is done`

	// Agent A asks for next work.
	if first, all := agentNext(t, p); first != `run /implement on T-1 "Reset-token table"` || !strings.Contains(all, blocked) {
		t.Fatalf("agent A's next:\n%s", all)
	}
	// Agent A starts T-1, and claims it.
	agentMove(t, p, "T-1", "in-progress", 0)
	if got := claimOf(t, p.Read(".jigflow/state/T-1.md")); got != "A" {
		t.Errorf("T-1 claim = %q, want A", got)
	}
	// Agent B asks for next work: T-1 is A's, and T-3 is still blocked.
	r := p.RunInSession("B", "next")
	if first := firstLine(r.Stdout); first != `run /implement on T-2 "Email template"` {
		t.Errorf("agent B's next = %q, want T-2", first)
	}
	for _, want := range []string{"T-1: claimed by agent session A", blocked} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("agent B's next should say %q:\n%s", want, r.Stdout)
		}
	}
	if got := focusOf(t, p, "B"); got != "T-2" {
		t.Errorf("agent B's Focus = %q, want T-2", got)
	}
	// Agent B tries to move T-1 anyway.
	if r := p.RunInSession("B", "move", "T-1", "in-review"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "T-1 is claimed by agent session A.") {
		t.Errorf("agent B's move of T-1 exited %d, want 1 refused by A's Claim; stderr: %s", r.ExitCode, r.Stderr)
	}
	// Agent B starts T-2.
	if r := p.RunInSession("B", "move", "T-2", "in-progress"); r.ExitCode != 0 {
		t.Fatalf("agent B's start of T-2 exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	if got := claimOf(t, p.Read(".jigflow/state/T-2.md")); got != "B" {
		t.Errorf("T-2 claim = %q, want B", got)
	}
	if first, _ := agentNext(t, p); first != `run /implement on T-1 "Reset-token table"` {
		t.Errorf("agent A's next = %q, want its own T-1", first)
	}
}

// The prototype's "Triage" walkthrough: an incoming bug goes through the
// triage labels. The agent can ask for more information, but only you decide
// who does the work; needs-triage is an Inbox, so agents file bugs into it
// without asking. The Claim disappears when I-1 waits on the reporter, and a
// ready-for-human issue never shows up in an agent's next.
func TestWalkthroughTriage(t *testing.T) {
	p := pocockPlaybook(t)

	// A reporter (you) files a bug.
	p.MustRun("create", "Issue", "--title", "Reset link expires instantly")
	// Agent A asks for next work.
	if first, _ := agentNext(t, p); first != `run /triage on I-1 "Reset link expires instantly"` {
		t.Fatalf("next = %q, want /triage on I-1", first)
	}
	// Agent A runs /triage and asks for more info: waiting on the reporter
	// is human work, so the Claim is released.
	agentMove(t, p, "I-1", "needs-info", 0)
	if got := claimOf(t, p.Read(".jigflow/state/I-1.md")); got != "" {
		t.Errorf("I-1 claim = %q in needs-info, want it released", got)
	}
	// Agent A asks for next work.
	if first, all := agentNext(t, p); first != "nothing for an agent to do" || !strings.Contains(all, `I-1: "needs-info" has no Binding, so it's human work`) {
		t.Fatalf("next with I-1 waiting on the reporter:\n%s", all)
	}
	// The reporter adds details: back to triage, where any session may pick it.
	p.MustRun("move", "I-1", "needs-triage")
	if first := firstLine(p.RunInSession("B", "next").Stdout); first != `run /triage on I-1 "Reset link expires instantly"` {
		t.Errorf("agent B's next = %q, want /triage on I-1", first)
	}
	// Agent A marks I-1 ready-for-agent itself.
	if r := agentMove(t, p, "I-1", "ready-for-agent", 1); !strings.Contains(r.Stderr, "is a Human Transition. An agent can only propose it") {
		t.Errorf("marking I-1 ready-for-agent should be refused as a Human Transition:\n%s", r.Stderr)
	}
	// You mark I-1 ready-for-human.
	term := p.StartInTerminal("move", "I-1", "ready-for-human")
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("marking I-1 ready-for-human exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}
	// Agent A asks for next work.
	if first, all := agentNext(t, p); first != "nothing for an agent to do" || !strings.Contains(all, `I-1: "ready-for-human" has no Binding, so it's human work`) {
		t.Fatalf("next with I-1 ready for a human:\n%s", all)
	}
	// Agent B finds another bug and files it (needs-triage is an Inbox).
	if r := p.RunInSession("B", "create", "Issue", "--title", "Token not invalidated after use"); r.ExitCode != 0 {
		t.Fatalf("agent B filing I-2 exited %d; stderr: %s", r.ExitCode, r.Stderr)
	}
	// Agent A asks for next work.
	if first, _ := agentNext(t, p); first != `run /triage on I-2 "Token not invalidated after use"` {
		t.Errorf("next = %q, want /triage on I-2", first)
	}
}
