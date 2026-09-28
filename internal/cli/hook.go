package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jigflow-ai/jigflow/internal/adapter"
	"github.com/jigflow-ai/jigflow/internal/engine"
)

// cmdHook runs as one of the hooks `jfl publish claude-code` sets up in
// Claude Code, with the hook's input on stdin. At SessionStart it gives the
// session's commands its session id as JFL_SESSION, through the file
// CLAUDE_ENV_FILE names; after each turn and at the end of the session it
// adds the usage Claude Code's transcripts record, and the Ledger doesn't
// have yet, to the Ledger.
//
// It is Claude Code that runs it, never an agent session: usage comes only
// from Claude Code's own records, not from numbers an agent reports (ADR
// 0007), so it is refused where JFL_SESSION is set.
func cmdHook(e *env, args []string) error {
	if len(args) != 1 || args[0] != adapter.ClaudeCode {
		return fmt.Errorf("%w: jfl hook %s", errUsage, adapter.ClaudeCode)
	}
	if e.actor.Agent() {
		return fmt.Errorf("jfl hook is run by Claude Code's hooks, which jfl publish %s sets up, never by an agent session (%s is set): the Ledger takes usage only from Claude Code's own records", adapter.ClaudeCode, SessionEnv)
	}
	// Claude Code runs hooks where the session is, which may be below the
	// project root; it names the root.
	if dir := e.getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		e.dir = dir
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".jigflow", "playbook.yaml")); err != nil {
		return fmt.Errorf("%s is not a JigFlow project: %w", e.dir, err)
	}
	h, err := adapter.ReadClaudeCodeHook(e.stdin, e.getenv)
	if err != nil {
		return err
	}
	if h.Event == "SessionStart" {
		return e.startSession(h.Session)
	}
	return e.recordUsage(h.Usage)
}

// startSession gives every command of the Claude Code session the jfl
// session id it is known by, its own, by adding it to the environment file
// Claude Code reads before each of them.
func (e *env) startSession(session string) error {
	path := e.getenv("CLAUDE_ENV_FILE")
	if path == "" {
		return errors.New("Claude Code gave the SessionStart hook no CLAUDE_ENV_FILE, so the session's commands can't be given " + SessionEnv)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "export %s=%s\n", SessionEnv, session); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// recordUsage adds to the Ledger the usage of each message it doesn't
// record yet.
func (e *env) recordUsage(us []engine.Usage) error {
	l, err := e.ledger().Read()
	if err != nil {
		return err
	}
	type message struct{ session, agent, id string }
	recorded := map[message]bool{}
	for _, u := range l.Usages {
		recorded[message{u.Session, u.Agent, u.Message}] = true
	}
	var fresh []engine.Usage
	for _, u := range us {
		if !recorded[message{u.Session, u.Agent, u.Message}] {
			fresh = append(fresh, u)
		}
	}
	return e.ledger().RecordUsage(e.now(), fresh)
}
