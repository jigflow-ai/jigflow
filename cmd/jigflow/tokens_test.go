package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// claudeCode points the project's commands at a fake Claude Code
// configuration directory, where Claude Code keeps its records, and returns
// it.
func claudeCode(t *testing.T, p *clitest.Project) string {
	t.Helper()
	dir := t.TempDir()
	p.Setenv("CLAUDE_CONFIG_DIR", dir)
	return dir
}

// assistant is one assistant message of a Claude Code transcript, as Claude
// Code writes it: when it was sent, its id, and the tokens the API reported.
type assistant struct {
	at, id                               string
	input, output, cacheRead, cacheWrite int
}

// line is the transcript line Claude Code writes for the message.
func (m assistant) line() string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":"A","message":{"id":%q,"type":"message","role":"assistant","model":"claude-opus-4-1","content":[{"type":"text","text":"working"}],"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"service_tier":"standard"}}}`,
		m.at, m.id, m.input, m.cacheWrite, m.cacheRead, m.output)
}

// transcript writes a Claude Code transcript of the session into its
// projects directory, with a user prompt and the assistant messages, and
// returns its path.
func transcript(t *testing.T, config, session string, messages ...assistant) string {
	t.Helper()
	lines := []string{`{"type":"user","timestamp":"2026-09-28T08:59:00.000Z","sessionId":"A","message":{"role":"user","content":"work on your own"}}`}
	for _, m := range messages {
		lines = append(lines, m.line())
	}
	path := filepath.Join(config, "projects", "-home-dev-project", session+".jsonl")
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hook runs jfl's Claude Code hook as Claude Code runs it after the
// session's turn: outside any agent session, with the hook's input on stdin.
func hook(t *testing.T, p *clitest.Project, event, session, transcript string) clitest.Result {
	t.Helper()
	in, err := json.Marshal(map[string]any{"session_id": session, "transcript_path": transcript, "cwd": p.Dir, "hook_event_name": event})
	if err != nil {
		t.Fatal(err)
	}
	return p.RunWithInput("", string(in), "hook", "claude-code")
}

func mustHook(t *testing.T, p *clitest.Project, event, session, transcript string) {
	t.Helper()
	if r := hook(t, p, event, session, transcript); r.ExitCode != 0 {
		t.Fatalf("jfl hook claude-code (%s) exited %d\nstdout: %s\nstderr: %s", event, r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestClaudeCodeTokenUsageIsChargedToTheArtifactInFocusWhenItHappened(t *testing.T) {
	p := ticketPlaybook(t)
	config := claudeCode(t, p)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.MustRun("create", "Ticket", "--title", "Signup page")
	p.At("2026-09-28T09:10:00Z")
	agentNext(t, p) // Focus: T-1
	p.At("2026-09-28T09:40:00Z")
	agentMove(t, p, "T-1", "in-progress", 0)
	agentMove(t, p, "T-1", "in-review", 0) // out of Focus
	p.At("2026-09-28T10:00:00Z")
	agentNext(t, p) // Focus: T-2

	path := transcript(t, config, "A",
		assistant{at: "2026-09-28T09:05:00.000Z", id: "msg_1", input: 3, output: 20, cacheRead: 0, cacheWrite: 1000},
		assistant{at: "2026-09-28T09:15:00.000Z", id: "msg_2", input: 5, output: 300, cacheRead: 4000, cacheWrite: 200},
		// Claude Code writes a line per content block of one message, each
		// with the message's usage.
		assistant{at: "2026-09-28T09:20:00.000Z", id: "msg_3", input: 7, output: 100, cacheRead: 5000, cacheWrite: 100},
		assistant{at: "2026-09-28T09:20:01.000Z", id: "msg_3", input: 7, output: 100, cacheRead: 5000, cacheWrite: 100},
		assistant{at: "2026-09-28T09:50:00.000Z", id: "msg_4", input: 1, output: 30, cacheRead: 6000, cacheWrite: 0},
		assistant{at: "2026-09-28T10:05:00.000Z", id: "msg_5", input: 2, output: 400, cacheRead: 6500, cacheWrite: 300},
	)
	p.At("2026-09-28T10:10:00Z")
	mustHook(t, p, "Stop", "A", path)

	r := p.MustRun("ledger")
	if got := block(r.Stdout, `T-1 Ticket "Login page"`); !slices.Contains(got, "tokens 12 input, 400 output, 9000 cache read, 300 cache write") {
		t.Errorf("T-1's time = %q, want msg_2 and msg_3's tokens", got)
	}
	if got := block(r.Stdout, `T-2 Ticket "Signup page"`); !slices.Contains(got, "tokens 2 input, 400 output, 6500 cache read, 300 cache write") {
		t.Errorf("T-2's time = %q, want msg_5's tokens", got)
	}
	if !slices.Contains(ledgerLines(r.Stdout), "Tokens unattributed: 4 input, 50 output, 6000 cache read, 1000 cache write") {
		t.Errorf("jfl ledger should charge msg_1 and msg_4, sent with nothing in Focus, to unattributed:\n%s", r.Stdout)
	}
}

func TestEachTurnsHookAddsOnlyTheMessagesTheLedgerDoesntHaveYet(t *testing.T) {
	p := ticketPlaybook(t)
	config := claudeCode(t, p)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p) // Focus: T-1
	first := assistant{at: "2026-09-28T09:05:00.000Z", id: "msg_1", input: 10, output: 100, cacheRead: 1000, cacheWrite: 10}
	path := transcript(t, config, "A", first)
	p.At("2026-09-28T09:06:00Z")
	mustHook(t, p, "Stop", "A", path)
	// The next turn appends to the transcript, and its Stop hook reads it all.
	transcript(t, config, "A", first, assistant{at: "2026-09-28T09:10:00.000Z", id: "msg_2", input: 1, output: 2, cacheRead: 3, cacheWrite: 4})
	p.At("2026-09-28T09:11:00Z")
	mustHook(t, p, "Stop", "A", path)
	mustHook(t, p, "SessionEnd", "A", path)

	r := p.MustRun("ledger")
	if got := block(r.Stdout, `T-1 Ticket "Login page"`); !slices.Contains(got, "tokens 11 input, 102 output, 1003 cache read, 14 cache write") {
		t.Errorf("T-1's time = %q, want msg_1 and msg_2 counted once each", got)
	}
}

func TestASubAgentsTranscriptIsReadWhenItStops(t *testing.T) {
	p := ticketPlaybook(t)
	config := claudeCode(t, p)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p) // Focus: T-1
	main := transcript(t, config, "A")
	sub := filepath.Join(config, "projects", "-home-dev-project", "A", "subagents", "agent-1.jsonl")
	writeFile(t, sub, assistant{at: "2026-09-28T09:05:00.000Z", id: "msg_9", input: 5, output: 50, cacheRead: 500, cacheWrite: 5}.line()+"\n")
	in, _ := json.Marshal(map[string]any{"session_id": "A", "transcript_path": main, "agent_transcript_path": sub, "hook_event_name": "SubagentStop"})
	p.At("2026-09-28T09:06:00Z")
	if r := p.RunWithInput("", string(in), "hook", "claude-code"); r.ExitCode != 0 {
		t.Fatalf("SubagentStop hook exited %d: %s", r.ExitCode, r.Stderr)
	}

	r := p.MustRun("ledger")
	if got := block(r.Stdout, `T-1 Ticket "Login page"`); !slices.Contains(got, "tokens 5 input, 50 output, 500 cache read, 5 cache write") {
		t.Errorf("T-1's time = %q, want the sub-agent's tokens", got)
	}
}

func TestAnAgentSessionCantRecordUsageThroughTheHook(t *testing.T) {
	p := ticketPlaybook(t)
	config := claudeCode(t, p)
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p)
	path := transcript(t, config, "A", assistant{at: "2026-09-28T09:05:00.000Z", id: "msg_1", input: 1, output: 1_000_000})
	in, _ := json.Marshal(map[string]any{"session_id": "A", "transcript_path": path, "hook_event_name": "Stop"})

	r := p.RunWithInput("A", string(in), "hook", "claude-code")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "never by an agent session") {
		t.Errorf("an agent session's jfl hook exited %d (%s), want it refused", r.ExitCode, r.Stderr)
	}
	if out := p.MustRun("ledger").Stdout; strings.Contains(out, "tokens") || strings.Contains(out, "Tokens") {
		t.Errorf("the Ledger records tokens an agent session handed it:\n%s", out)
	}
}

func TestTheHookReadsOnlyClaudeCodesOwnTranscripts(t *testing.T) {
	p := ticketPlaybook(t)
	claudeCode(t, p)
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p)
	forged := filepath.Join(p.Dir, "usage.jsonl")
	writeFile(t, forged, assistant{at: "2026-09-28T09:05:00.000Z", id: "msg_1", input: 1, output: 1_000_000}.line()+"\n")

	r := hook(t, p, "Stop", "A", forged)
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "isn't one of Claude Code's transcripts") {
		t.Errorf("jfl hook on a file outside Claude Code's records exited %d (%s), want it refused", r.ExitCode, r.Stderr)
	}
	if out := p.MustRun("ledger").Stdout; strings.Contains(out, "Tokens") {
		t.Errorf("the Ledger records tokens from outside Claude Code's records:\n%s", out)
	}
}

func TestAgentsWithoutAUsageSourceRecordTimeOnly(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	agentNext(t, p)
	p.At("2026-09-28T09:30:00Z")
	agentMove(t, p, "T-1", "in-progress", 0)
	agentMove(t, p, "T-1", "in-review", 0)

	r := p.MustRun("ledger")
	if got := block(r.Stdout, `T-1 Ticket "Login page"`); !slices.Contains(got, "agent time 30m") {
		t.Errorf("T-1's time = %q, want its 30m of agent time", got)
	}
	if strings.Contains(r.Stdout, "tokens") || strings.Contains(r.Stdout, "Tokens") {
		t.Errorf("jfl ledger shows tokens no agent's records gave it:\n%s", r.Stdout)
	}
}

func TestSessionStartGivesTheSessionsCommandsItsClaudeCodeSessionIdAsJFL_SESSION(t *testing.T) {
	// So the Focus jfl next sets is the one Claude Code's usage is charged to.
	p := ticketPlaybook(t)
	claudeCode(t, p)
	envFile := filepath.Join(t.TempDir(), "session-env.sh")
	p.Setenv("CLAUDE_ENV_FILE", envFile)

	in := `{"session_id":"5b2c9d1e-0f3a-4c6b-9e8d-7a1b2c3d4e5f","transcript_path":"/nowhere.jsonl","hook_event_name":"SessionStart","source":"startup"}`
	if r := p.RunWithInput("", in, "hook", "claude-code"); r.ExitCode != 0 {
		t.Fatalf("SessionStart hook exited %d: %s", r.ExitCode, r.Stderr)
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "export JFL_SESSION=5b2c9d1e-0f3a-4c6b-9e8d-7a1b2c3d4e5f\n"; got != want {
		t.Errorf("CLAUDE_ENV_FILE = %q, want %q", got, want)
	}
}
