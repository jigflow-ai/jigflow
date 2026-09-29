package main_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"go.yaml.in/yaml/v3"
)

// block returns the lines of out indented under the line reading header,
// each trimmed and with the spaces that align its columns collapsed to one,
// or nil when out has no such line.
func block(out, header string) []string {
	lines := strings.Split(out, "\n")
	indent := func(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }
	collapse := func(l string) string { return strings.Join(strings.Fields(l), " ") }
	for i, l := range lines {
		if collapse(l) != header {
			continue
		}
		got := []string{}
		for _, m := range lines[i+1:] {
			if strings.TrimSpace(m) == "" || indent(m) <= indent(l) {
				break
			}
			got = append(got, collapse(m))
		}
		return got
	}
	return nil
}

// ledgerLines returns the lines of out, trimmed and with the spaces that
// align its columns collapsed to one.
func ledgerLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		lines = append(lines, strings.Join(strings.Fields(l), " "))
	}
	return lines
}

// wantBlock fails the test unless the lines under header in the output of
// jfl ledger are want.
func wantBlock(t *testing.T, out, header string, want ...string) {
	t.Helper()
	if got := block(out, header); !slices.Equal(got, want) {
		t.Errorf("jfl ledger under %q:\n  got  %q\n  want %q\nin:\n%s", header, got, want, out)
	}
}

func TestTheLedgerRecordsTheTimeEachArtifactSpendsInEachStatus(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.At("2026-09-28T10:30:00Z")
	p.MustRun("move", "T-1", "in-progress")
	p.At("2026-09-28T12:30:00Z")
	p.MustRun("move", "T-1", "in-review")
	p.At("2026-09-28T13:15:00Z")

	r := p.MustRun("ledger")
	wantBlock(t, r.Stdout, `T-1 Ticket "Login page"`,
		"ready-for-agent 1h30m",
		"in-progress 2h",
		"in-review 45m so far",
	)
}

func TestAgentSessionTimeBetweenFocusChangesIsChargedToTheArtifactInFocusOrToUnattributed(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.MustRun("create", "Ticket", "--title", "Signup page")
	agentNext(t, p) // Focus: T-1
	p.At("2026-09-28T09:10:00Z")
	agentMove(t, p, "T-1", "in-progress", 0)
	p.At("2026-09-28T09:40:00Z")
	agentMove(t, p, "T-1", "in-review", 0) // no Binding: out of Focus
	p.At("2026-09-28T09:55:00Z")
	agentNext(t, p) // Focus: T-2
	p.At("2026-09-28T10:15:00Z")
	agentNext(t, p) // Focus: T-2 still
	p.At("2026-09-28T10:20:00Z")
	agentMove(t, p, "T-2", "in-progress", 0)
	agentMove(t, p, "T-2", "in-review", 0)
	p.At("2026-09-28T11:00:00Z")

	r := p.MustRun("ledger")
	if got := block(r.Stdout, `T-1 Ticket "Login page"`); !slices.Contains(got, "agent time 40m") {
		t.Errorf("T-1's time = %q, want 40m of agent time", got)
	}
	if got := block(r.Stdout, `T-2 Ticket "Signup page"`); !slices.Contains(got, "agent time 25m") {
		t.Errorf("T-2's time = %q, want 25m of agent time", got)
	}
	if !slices.Contains(ledgerLines(r.Stdout), "Agent time unattributed: 15m") {
		t.Errorf("jfl ledger should charge the 15m with nothing in Focus to unattributed:\n%s", r.Stdout)
	}
}

func TestTheLedgerSumsTheTimeInEachStatusOverEveryArtifact(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.At("2026-09-28T10:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Signup page")
	p.MustRun("move", "T-1", "in-progress")
	p.At("2026-09-28T11:00:00Z")
	p.MustRun("move", "T-1", "in-review")
	p.At("2026-09-28T11:30:00Z")
	p.MustRun("move", "T-1", "done")
	p.At("2026-09-28T14:00:00Z")

	r := p.MustRun("ledger")
	// Time in done, where no work waits, isn't counted.
	wantBlock(t, r.Stdout, `T-1 Ticket "Login page"`,
		"ready-for-agent 1h",
		"in-progress 1h",
		"in-review 30m",
	)
	wantBlock(t, r.Stdout, "Ticket",
		"ready-for-agent 5h over 2 Artifacts, 1 there now",
		"in-progress 1h over 1 Artifact",
		"in-review 30m over 1 Artifact",
	)
}

func TestAnApprovedProposalsCreationsAndTransitionsAreInTheLedger(t *testing.T) {
	p := pocockPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", "summary: one ticket\nitems:\n  - {create: Ticket, ref: a, title: Reset-token table, links: {part_of: [S-1]}}\n  - {move: S-1, to: ticketed}\n")
	p.MustRun("propose", "breakdown.yaml")
	p.At("2026-09-28T10:00:00Z")
	approveInTerminal(t, p, "P-1")
	p.At("2026-09-28T10:45:00Z")

	r := p.MustRun("ledger")
	wantBlock(t, r.Stdout, `S-1 Spec "Password reset by email"`, "ready-for-agent 1h")
	wantBlock(t, r.Stdout, `T-1 Ticket "Reset-token table"`, "ready-for-agent 45m so far")
}

func TestAMigrationEndsTheTimeInTheStatusItMapsAway(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T08:30:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("move", "T-1", "in-progress")
	renameInProgress(p, ".jigflow/types/ticket.yaml")
	p.Write(".jigflow/migrations/doing.yaml", renameMigration)
	p.At("2026-09-28T11:00:00Z")
	p.MustRun("migrate")
	p.At("2026-09-28T11:20:00Z")

	r := p.MustRun("ledger")
	// The Status the Playbook no longer declares comes after those it does.
	wantBlock(t, r.Stdout, `T-1 Ticket "Login page"`,
		"ready-for-agent 30m",
		"doing 20m so far",
		"in-progress 2h",
	)
}

func TestTheLedgerOfArtifactsKeptInATrackerIsKeptInTheRepository(t *testing.T) {
	p := trackerPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page", "--status", "ready-for-agent")
	agentNext(t, p)
	p.At("2026-09-28T09:20:00Z")
	agentMove(t, p, "T-41", "in-progress", 0)
	p.At("2026-09-28T10:00:00Z")
	agentMove(t, p, "T-41", "in-review", 0)
	p.At("2026-09-28T10:05:00Z")
	before := len(calls(t, p, "list")) + len(calls(t, p, "get"))

	r := p.MustRun("ledger")
	wantBlock(t, r.Stdout, `T-41 Ticket "Login page"`,
		"ready-for-agent 20m",
		"in-progress 40m",
		"in-review 5m so far",
		"agent time 1h",
	)
	if after := len(calls(t, p, "list")) + len(calls(t, p, "get")); after != before {
		t.Errorf("jfl ledger read the tracker (%d requests); the Ledger is in the repository", after-before)
	}
}

func TestTheLedgerIsCommittedAndParallelSessionsOnBranchesMergeWithoutConflicts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	p := ticketPlaybook(t)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = p.Dir
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	commit := func(msg string) {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "-m", msg)
	}
	git("init", "-q", "-b", "main")
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.MustRun("create", "Ticket", "--title", "Signup page")
	commit("two tickets")

	// Agent session A works on T-1 on a branch, while B works on T-2 on
	// main, each recording into the Ledger at the same time.
	git("checkout", "-q", "-b", "a")
	agentNext(t, p)
	p.At("2026-09-28T09:30:00Z")
	agentMove(t, p, "T-1", "in-progress", 0)
	p.At("2026-09-28T10:00:00Z")
	agentMove(t, p, "T-1", "in-review", 0)
	commit("A's work")
	git("checkout", "-q", "main")
	p.At("2026-09-28T09:30:00Z")
	if r := p.RunInSession("B", "move", "T-2", "in-progress"); r.ExitCode != 0 {
		t.Fatalf("agent B's move exited %d: %s", r.ExitCode, r.Stderr)
	}
	commit("B's work")

	git("merge", "-q", "--no-edit", "a")
	// Two creations, and each session's Status and Focus changes.
	if n := len(strings.Fields(git("ls-files", ".jigflow/ledger"))); n != 7 {
		t.Errorf("git tracks %d Ledger entries after the merge, want 7", n)
	}
	p.At("2026-09-28T10:30:00Z")
	r := p.MustRun("ledger")
	wantBlock(t, r.Stdout, `T-1 Ticket "Login page"`,
		"ready-for-agent 30m",
		"in-progress 30m",
		"in-review 30m so far",
		"agent time 1h",
	)
	wantBlock(t, r.Stdout, `T-2 Ticket "Signup page"`,
		"ready-for-agent 30m",
		"in-progress 1h so far",
	)
}

func TestAShippedJflRecordsTheRealTimeWhateverTheEnvironmentSays(t *testing.T) {
	// Only the tests' build reads its clock from the environment, so no
	// agent can set the time the Ledger records (ADR 0007).
	shipped, err := bin.BuildHelper("github.com/jigflow-ai/jigflow/cmd/jigflow", "jigflow-shipped")
	if err != nil {
		t.Fatal(err)
	}
	p := ticketPlaybook(t)
	p.At("2001-01-01T09:00:00Z")
	if r := p.RunAs(shipped, "create", "Ticket", "--title", "Login page"); r.ExitCode != 0 {
		t.Fatalf("create exited %d: %s", r.ExitCode, r.Stderr)
	}
	entries, err := os.ReadDir(filepath.Join(p.Dir, ".jigflow/ledger"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the Ledger holds %d entries (%v), want the creation's", len(entries), err)
	}
	if got := p.Read(".jigflow/ledger/" + entries[0].Name()); strings.Contains(got, "2001-01-01") {
		t.Errorf("a shipped jfl recorded the time the environment set:\n%s", got)
	}
}

func TestAStoppedAutopilotEndsTheTimeChargedToItsFocus(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	autopilot(t, p)
	// The Skill it handed out doesn't move T-1 on, so autopilot stops.
	p.At("2026-09-28T09:25:00Z")
	autopilot(t, p)
	p.At("2026-09-28T09:40:00Z")
	agentNext(t, p)

	r := p.MustRun("ledger")
	if got := block(r.Stdout, `T-1 Ticket "Login page"`); !slices.Contains(got, "agent time 25m") {
		t.Errorf("T-1's time = %q, want the 25m until autopilot stopped", got)
	}
	if !slices.Contains(ledgerLines(r.Stdout), "Agent time unattributed: 15m") {
		t.Errorf("jfl ledger should charge the 15m after autopilot stopped to unattributed:\n%s", r.Stdout)
	}
}

// ledgerEntries returns the Ledger's entry files, by name.
func ledgerEntries(t *testing.T, p *clitest.Project) map[string]string {
	t.Helper()
	dirents, err := os.ReadDir(filepath.Join(p.Dir, ".jigflow/ledger"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range dirents {
		out[d.Name()] = p.Read(".jigflow/ledger/" + d.Name())
	}
	return out
}

// entriesVia returns the Ledger's Status-change entries that move into the
// Status to, split by the channel of their Confirmation: "" for none.
func entriesVia(t *testing.T, p *clitest.Project, to string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for name, text := range ledgerEntries(t, p) {
		var e struct{ To, Via string }
		if err := yaml.Unmarshal([]byte(text), &e); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if e.To == to {
			out[e.Via]++
		}
	}
	return out
}

func TestAHumanTransitionConfirmedInATerminalIsRecordedAsConfirmedThere(t *testing.T) {
	p := mergePlaybook(t)
	p.At("2026-09-28T10:00:00Z")

	term := p.StartInTerminal("move", "T-1", "done")
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("the confirmed Human Transition exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}

	if got := entriesVia(t, p, "done"); got["terminal"] != 1 || len(got) != 1 {
		t.Errorf("entries moving T-1 to done, by channel = %v, want one via terminal", got)
	}
	// The creation and the ordinary Transition before it needed no
	// Confirmation.
	for _, to := range []string{"in-review", "ready-to-merge"} {
		if got := entriesVia(t, p, to); got[""] != 1 || len(got) != 1 {
			t.Errorf("entries into %s, by channel = %v, want one with none", to, got)
		}
	}
	wantBlock(t, p.MustRun("ledger").Stdout, "Confirmations:",
		"2026-09-28 10:00 T-1 ready-to-merge → done via terminal",
	)
}

func TestEveryStatusChangeOfAProposalApprovedInATerminalIsRecordedAsConfirmedThere(t *testing.T) {
	p := pocockPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	p.Write("breakdown.yaml", "summary: one ticket\nitems:\n  - {create: Ticket, ref: a, title: Reset-token table, links: {part_of: [S-1]}}\n  - {move: S-1, to: ticketed}\n")
	p.RunInSession("A", "propose", "breakdown.yaml")
	p.At("2026-09-28T10:00:00Z")
	approveInTerminal(t, p, "P-1")

	if got := entriesVia(t, p, "ticketed"); got["terminal"] != 1 || len(got) != 1 {
		t.Errorf("entries moving S-1 to ticketed, by channel = %v, want one via terminal", got)
	}
	// The Spec, created by a person, and the Ticket, created by the
	// Proposal, both start in ready-for-agent.
	if got := entriesVia(t, p, "ready-for-agent"); got["terminal"] != 1 || got[""] != 1 || len(got) != 2 {
		t.Errorf("entries into ready-for-agent, by channel = %v, want the Proposal's via terminal and the person's creation with none", got)
	}
	wantBlock(t, p.MustRun("ledger").Stdout, "Confirmations:",
		"2026-09-28 10:00 T-1 created in ready-for-agent via terminal",
		"2026-09-28 10:00 S-1 ready-for-agent → ticketed via terminal",
	)
}

func TestAHumanTransitionMadeInTheDashboardIsRecordedAsConfirmedThere(t *testing.T) {
	p := mergePlaybook(t)
	p.At("2026-09-28T10:00:00Z")
	ui := p.StartUI()

	if page := submit(t, ui, rowHTML(t, get(t, ui, "/"), "T-1"), "→ done", nil); page.Status != http.StatusOK {
		t.Fatalf("moving T-1 to done: status %d\n%s", page.Status, text(page.HTML))
	}

	if got := entriesVia(t, p, "done"); got["dashboard"] != 1 || len(got) != 1 {
		t.Errorf("entries moving T-1 to done, by channel = %v, want one via dashboard", got)
	}
	wantBlock(t, p.MustRun("ledger").Stdout, "Confirmations:",
		"2026-09-28 10:00 T-1 ready-to-merge → done via dashboard",
	)
}

func TestEveryStatusChangeOfAProposalApprovedInTheDashboardIsRecordedAsConfirmedThere(t *testing.T) {
	p := proposedBreakdown(t)
	p.At("2026-09-28T10:00:00Z")
	ui := p.StartUI()

	page := submit(t, ui, section(t, get(t, ui, "/"), "Pending Proposals"), "Approve all 4 changes", nil)
	if page.Status != http.StatusOK {
		t.Fatalf("approving P-1: status %d\n%s", page.Status, text(page.HTML))
	}

	if got := entriesVia(t, p, "ticketed"); got["dashboard"] != 1 || len(got) != 1 {
		t.Errorf("entries moving S-1 to ticketed, by channel = %v, want one via dashboard", got)
	}
	wantBlock(t, p.MustRun("ledger").Stdout, "Confirmations:",
		"2026-09-28 10:00 T-1 created in ready-for-agent via dashboard",
		"2026-09-28 10:00 T-2 created in ready-for-agent via dashboard",
		"2026-09-28 10:00 T-3 created in ready-for-agent via dashboard",
		"2026-09-28 10:00 S-1 ready-for-agent → ticketed via dashboard",
	)
}

func TestLedgerEntriesWrittenBeforeChannelsWereRecordedStillRead(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/ledger/20260928T090000.000000000Z-0000cafe-0001.yaml",
		"at: 2026-09-28T09:00:00Z\nartifact: T-1\ntype: Ticket\ntitle: Login page\nto: ready-for-agent\n")
	p.Write(".jigflow/ledger/20260928T100000.000000000Z-0000cafe-0002.yaml",
		"at: 2026-09-28T10:00:00Z\nartifact: T-1\ntype: Ticket\ntitle: Login page\nfrom: ready-for-agent\nto: in-progress\n")
	p.At("2026-09-28T10:30:00Z")

	r := p.MustRun("ledger")
	wantBlock(t, r.Stdout, `T-1 Ticket "Login page"`,
		"ready-for-agent 1h",
		"in-progress 30m so far",
	)
	if strings.Contains(r.Stdout, "Confirmations:") {
		t.Errorf("no entry records a Confirmation, but jfl ledger lists some:\n%s", r.Stdout)
	}
}

func TestRecordingConfirmationsKeepsTheLedgerOneFilePerEntryOnlyEverAdded(t *testing.T) {
	p := mergePlaybook(t)
	before := ledgerEntries(t, p)

	term := p.StartInTerminal("move", "T-1", "done")
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("the confirmed Human Transition exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}

	after := ledgerEntries(t, p)
	if len(after) != len(before)+1 {
		t.Errorf("the Ledger holds %d entries after the move, want %d: one more", len(after), len(before)+1)
	}
	for name, e := range before {
		if after[name] != e {
			t.Errorf("entry %s changed:\n%s\nwas:\n%s", name, after[name], e)
		}
	}
}
