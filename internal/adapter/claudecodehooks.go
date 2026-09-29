package adapter

import "fmt"

// claudeCodeSettings is the project's shared Claude Code settings file,
// committed, where the hooks jfl publishes go. The rest of it is the
// person's.
const claudeCodeSettings = ".claude/settings.json"

// hookCommand is the command of every hook jfl publishes; it is how a
// publish tells jfl's hooks from the person's.
const hookCommand = "jfl hook " + ClaudeCode

// hookEvents are the Claude Code hook events jfl's hook runs at: the start
// of a session, to give it JFL_SESSION, and the end of each turn, of each
// sub-agent and of the session, to read the usage the transcripts record.
var hookEvents = []string{"SessionStart", "Stop", "SubagentStop", "SessionEnd"}

// claudeCodeHooks is the settings file with jfl's hooks in it.
func claudeCodeHooks() File {
	return File{Path: claudeCodeSettings, Merge: func(old string) (string, error) { return withHooks(old, true) }}
}

// withHooks is the Claude Code settings in content with jfl's hooks, and
// only those, taken out, then, if add, put back in once at each of
// hookEvents. Everything else is kept, though its keys are written back in
// order. It is empty when nothing is left.
func withHooks(content string, add bool) (string, error) {
	settings, err := decodeObject(claudeCodeSettings, content)
	if err != nil {
		return "", err
	}
	hooks := map[string]any{}
	if h, ok := settings["hooks"]; ok {
		if hooks, ok = h.(map[string]any); !ok {
			return "", fmt.Errorf("%s: hooks is not an object", claudeCodeSettings)
		}
	}
	for event, groups := range hooks {
		kept, err := withoutJfl(groups)
		if err != nil {
			return "", fmt.Errorf("%s: hooks.%s: %w", claudeCodeSettings, event, err)
		}
		hooks[event] = kept
	}
	if add {
		for _, event := range hookEvents {
			groups, _ := hooks[event].([]any)
			hooks[event] = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand}}})
		}
	}
	for event, groups := range hooks {
		if gs, ok := groups.([]any); ok && len(gs) == 0 {
			delete(hooks, event)
		}
	}
	if len(hooks) > 0 {
		settings["hooks"] = hooks
	} else {
		delete(settings, "hooks")
	}
	return encodeObject(settings)
}

// withoutJfl is the matcher groups of one hook event without jfl's hooks,
// and without the groups that only held jfl's.
func withoutJfl(groups any) ([]any, error) {
	gs, ok := groups.([]any)
	if !ok {
		return nil, fmt.Errorf("not a list of matcher groups")
	}
	var kept []any
	for _, g := range gs {
		group, ok := g.(map[string]any)
		if !ok {
			kept = append(kept, g)
			continue
		}
		hs, ok := group["hooks"].([]any)
		if !ok {
			kept = append(kept, g)
			continue
		}
		var mine []any
		for _, h := range hs {
			if hook, ok := h.(map[string]any); ok && hook["command"] == hookCommand {
				continue
			}
			mine = append(mine, h)
		}
		if len(mine) == 0 && len(hs) > 0 {
			continue
		}
		if mine == nil {
			mine = []any{}
		}
		group["hooks"] = mine
		kept = append(kept, group)
	}
	if kept == nil {
		kept = []any{}
	}
	return kept, nil
}
