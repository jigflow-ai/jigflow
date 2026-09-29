package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// formHookEvents are the Claude Code hook events that reach the
// Confirmation form jfl's MCP server asks the client to show (ADR 0024),
// and what a hook at each can do with it.
var formHookEvents = []struct{ event, does string }{
	{"Elicitation", "answer the Confirmation form without showing it to you"},
	{"ElicitationResult", "change the answer you give in the Confirmation form"},
}

// FormHookWarnings are warnings about the Claude Code hooks, in the
// project rooted at root and in the user's settings, that can answer or
// change the answer to the Confirmation form jfl's MCP server asks for:
// an Elicitation or ElicitationResult hook whose matcher matches jfl's
// server name. Such a hook can approve Proposals and make Human
// Transitions in the person's place, and the Ledger records them as
// Confirmations through the agent's client all the same. They are only
// warned about: Human Transitions are a guardrail, not a security
// boundary (ADR 0003). A settings file that isn't there is no warning; one
// that can't be read or parsed is one, since what it holds can't be told.
func FormHookWarnings(root string, getenv func(string) string) []string {
	files := []settingsFile{
		{filepath.Join(root, claudeCodeSettings), claudeCodeSettings},
		{filepath.Join(root, claudeCodeLocalSettings), claudeCodeLocalSettings},
	}
	if user := claudeCodeUserSettings(getenv); user != "" {
		files = append(files, settingsFile{user, user})
	}
	var warnings []string
	read := map[string]bool{}
	for _, f := range files {
		// The user's settings are the project's when the project is the
		// user's home: read each file once.
		key := filepath.Clean(f.path)
		if abs, err := filepath.Abs(key); err == nil {
			key = abs
		}
		if read[key] {
			continue
		}
		read[key] = true
		warnings = append(warnings, f.formHooks()...)
	}
	return warnings
}

// settingsFile is a Claude Code settings file at path, which warnings name
// as name: relative to the project for the project's own.
type settingsFile struct{ path, name string }

// claudeCodeLocalSettings is the project's own Claude Code settings file,
// kept out of version control, which jfl only reads.
const claudeCodeLocalSettings = ".claude/settings.local.json"

// claudeCodeUserSettings is the user's Claude Code settings file, or ""
// when neither CLAUDE_CONFIG_DIR nor HOME says where it is.
func claudeCodeUserSettings(getenv func(string) string) string {
	if config := getenv("CLAUDE_CONFIG_DIR"); config != "" {
		return filepath.Join(config, "settings.json")
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".claude", "settings.json")
	}
	return ""
}

// formHooks are the warnings about the form hooks in the settings file.
func (f settingsFile) formHooks() []string {
	content, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	cantTell := func(err error) []string {
		return []string{fmt.Sprintf("can't tell whether %s has hooks that answer the Confirmation form in your place: %v", f.name, err)}
	}
	if err != nil {
		return cantTell(err)
	}
	settings, err := decodeObject(f.name, string(content))
	if err != nil {
		return cantTell(err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	var warnings []string
	for _, e := range formHookEvents {
		groups, _ := hooks[e.event].([]any)
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				continue
			}
			matcher, _ := group["matcher"].(string)
			if !matchesServer(matcher, mcpServerName) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"%s has an %s hook whose matcher %q matches jfl's MCP server (%s): it can %s, and so approve Proposals and make Human Transitions in your place; the Ledger records them as Confirmations given in the agent's client",
				f.name, e.event, matcher, whatRuns(group), e.does))
		}
	}
	return warnings
}

// whatRuns names the hooks of a matcher group by what they run.
func whatRuns(group map[string]any) string {
	var cs []string
	hs, _ := group["hooks"].([]any)
	for _, h := range hs {
		hook, _ := h.(map[string]any)
		switch {
		case hook["command"] != nil:
			cs = append(cs, fmt.Sprintf("runs %v", hook["command"]))
		case hook["url"] != nil:
			cs = append(cs, fmt.Sprintf("calls %v", hook["url"]))
		case hook["type"] != nil:
			cs = append(cs, fmt.Sprintf("a %v hook", hook["type"]))
		}
	}
	if len(cs) == 0 {
		return "no hooks yet"
	}
	return strings.Join(cs, "; ")
}

// exactMatcher is a matcher Claude Code compares as an exact name, or a
// list of them separated by "|" or ",": one of only letters, digits, "_",
// "-", spaces, "," and "|". Any other matcher is a regular expression.
var exactMatcher = regexp.MustCompile(`^[A-Za-z0-9_\- ,|]*$`)

// matchesServer reports whether a Claude Code hook's matcher matches the
// MCP server name, as Claude Code matches it: "*" or "" matches every
// server, a list of exact names each name in it, and anything else is an
// unanchored regular expression. Claude Code reads it as JavaScript does,
// and Go's syntax differs at the edges: one Go can't read, such as one with
// a look-ahead, is taken to match, since it may. A matcher that isn't a
// string is taken as "", which matches every server.
func matchesServer(matcher, name string) bool {
	if matcher == "*" || matcher == "" {
		return true
	}
	if exactMatcher.MatchString(matcher) {
		for _, n := range strings.FieldsFunc(matcher, func(r rune) bool { return r == '|' || r == ',' }) {
			if strings.TrimSpace(n) == name {
				return true
			}
		}
		return false
	}
	re, err := regexp.Compile(matcher)
	return err != nil || re.MatchString(name)
}
