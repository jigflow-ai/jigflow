package adapter

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// ClaudeCode is the name the Claude Code Adapter goes by, in `jfl publish`,
// `jfl hook` and the Ledger's usage entries.
const ClaudeCode = "claude-code"

// ClaudeCodeHook is what one of the Claude Code hooks jfl publishes was
// given: the event, the Claude Code session, and the usage the transcripts
// it names record.
type ClaudeCodeHook struct {
	Event   string // e.g. SessionStart or Stop
	Session string // Claude Code's session id, which is also the jfl session's
	Usage   []engine.Usage
}

// hookInput is the JSON object Claude Code writes to a hook command's
// stdin. Only the fields jfl reads are here; Claude Code sends others.
type hookInput struct {
	Session    string `json:"session_id"`
	Transcript string `json:"transcript_path"`
	// A sub-agent's own transcript, given to SubagentStop.
	AgentTranscript string `json:"agent_transcript_path"`
	Event           string `json:"hook_event_name"`
}

// ReadClaudeCodeHook reads the input Claude Code gave one of its hooks and,
// for every event but SessionStart, the usage of each assistant message in
// the transcripts it names. These are Claude Code's own records, read only
// inside the directory where Claude Code keeps them (CLAUDE_CONFIG_DIR, or
// ~/.claude, then projects/), and never numbers the agent reports (ADR
// 0007). A transcript that isn't there yet has no usage.
func ReadClaudeCodeHook(in io.Reader, getenv func(string) string) (ClaudeCodeHook, error) {
	var h ClaudeCodeHook
	var hi hookInput
	if err := json.NewDecoder(in).Decode(&hi); err != nil {
		return h, fmt.Errorf("reading Claude Code's hook input: %w", err)
	}
	if !safeSession(hi.Session) {
		return h, fmt.Errorf("Claude Code's hook input has no usable session_id (%q)", hi.Session)
	}
	h.Event, h.Session = hi.Event, hi.Session
	if h.Event == "SessionStart" {
		return h, nil
	}
	records, err := claudeCodeRecords(getenv)
	if err != nil {
		return h, err
	}
	seen := map[string]bool{}
	for _, path := range []string{hi.Transcript, hi.AgentTranscript} {
		if path == "" {
			continue
		}
		us, err := readTranscript(records, path, h.Session)
		if err != nil {
			return h, err
		}
		for _, u := range us {
			if !seen[u.Message] {
				seen[u.Message] = true
				h.Usage = append(h.Usage, u)
			}
		}
	}
	return h, nil
}

// safeSession reports whether a Claude Code session id can be a jfl
// session id and be written into a shell script as it is.
func safeSession(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// claudeCodeRecords is the directory where Claude Code keeps its sessions'
// transcripts, one directory per project.
func claudeCodeRecords(getenv func(string) string) (string, error) {
	config := getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("can't find Claude Code's records: neither CLAUDE_CONFIG_DIR nor HOME is set")
		}
		config = filepath.Join(home, ".claude")
	}
	return filepath.Join(config, "projects"), nil
}

// transcriptLine is one line of a Claude Code transcript, a JSON object per
// line. Only the fields jfl reads are here; lines of other kinds, or that
// don't parse, are skipped.
type transcriptLine struct {
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	UUID      string `json:"uuid"`
	Message   *struct {
		ID    string `json:"id"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// readTranscript reads the usage of each message in the Claude Code
// transcript at path, which must be inside records, for the session.
//
// Claude Code writes a line per content block of a message, each carrying
// the message's usage, so a message is counted once, with the usage of its
// last line. A message is known by its API id, or else its request's or its
// line's; a message with no usage recorded, such as one Claude Code made up
// itself, is skipped. One whose time doesn't parse has none, and is charged
// to unattributed.
func readTranscript(records, path, session string) ([]engine.Usage, error) {
	if err := inside(records, path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []engine.Usage
	at := map[string]int{} // index in out, by message
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var tl transcriptLine
			if json.Unmarshal(line, &tl) == nil && tl.Message != nil && tl.Message.Usage != nil {
				u := tl.Message.Usage
				tokens := engine.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}
				id := firstOf(tl.Message.ID, tl.RequestID, tl.UUID, fmt.Sprintf("%s:%d", filepath.Base(path), n))
				when, _ := time.Parse(time.RFC3339Nano, tl.Timestamp)
				usage := engine.Usage{At: when, Session: session, Agent: ClaudeCode, Message: id, Tokens: tokens}
				switch i, ok := at[id]; {
				case ok:
					usage.At = out[i].At // sent when its first line was written
					out[i] = usage
				case tokens != engine.Tokens{}:
					at[id] = len(out)
					out = append(out, usage)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading Claude Code's transcript %s: %w", path, err)
		}
	}
}

// inside refuses a path outside dir, following symbolic links.
func inside(dir, path string) error {
	refuse := fmt.Errorf("%s isn't one of Claude Code's transcripts, in %s: jfl reads usage only from Claude Code's own records", path, dir)
	if !filepath.IsAbs(path) {
		return refuse
	}
	d, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return refuse
	}
	p, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return refuse
	}
	p = filepath.Join(p, filepath.Base(path))
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return refuse
	}
	rel, err := filepath.Rel(d, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return refuse
	}
	return nil
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
