package adapter

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Installs reports whether a can install jfl's own Skills.
func (a *Adapter) Installs() bool { return a.install != nil }

// Install writes jfl's own Skills, those of no Playbook, where a's coding
// agent finds the Skills a user has in every project, and returns the
// absolute paths of the files it created or whose content it changed:
// none when they are already there. It writes nothing into any project. It
// refuses, changing nothing, to overwrite a file a person wrote, and fails
// when a has no such place, saying why.
func (a *Adapter) Install(getenv func(string) string) ([]string, error) {
	if a.install == nil {
		return nil, fmt.Errorf("the %s Adapter can't install jfl's own Skills: %s", a.Name, a.NoInstall)
	}
	dir, files, err := a.install(getenv)
	if err != nil {
		return nil, err
	}
	var write []File
	for _, f := range files {
		old, err := os.ReadFile(abs(dir, f.Path))
		switch {
		case errors.Is(err, os.ErrNotExist):
			write = append(write, f)
		case err != nil:
			return nil, err
		case string(old) == f.Content:
			// Installed already, by this jfl or one shipping the same.
		case !strings.HasPrefix(string(old), installedHeader):
			return nil, fmt.Errorf("%s was not installed by jfl, so it isn't replaced; move it away and install again", abs(dir, f.Path))
		default:
			write = append(write, f)
		}
	}
	var wrote []string
	for _, f := range write {
		dst := abs(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o644); err != nil {
			return wrote, err
		}
		wrote = append(wrote, dst)
	}
	return wrote, nil
}

// installedHeader starts every Skill jfl install writes, which a later
// install may replace: the frontmatter, with a comment the agent never
// reads as the Skill's instructions.
const installedHeader = "---\n# Installed by jfl install"

// jigflowInit is the Skill that sets a repository up with JigFlow, from
// the agent, by running jfl init as an agent session with every answer as
// a flag (ADR 0026).
const jigflowInit = "jigflow-init"

// jigflowInitPrompt is the body of the jigflow-init Skill.
//
//go:embed jigflow-init.md
var jigflowInitPrompt string

// jigflowInitDescription says what jigflow-init is for, to the person
// choosing a Skill to start.
const jigflowInitDescription = "Set this repository up with JigFlow: suggest a Playbook, work out its settings with you, and run jfl init for you."

// claudeCodeInstall installs jigflow-init as a Claude Code Skill only a
// person starts, in the user's Skills: under CLAUDE_CONFIG_DIR, or else
// ~/.claude. Claude Code loads a Skill written there during a session
// without a restart.
func claudeCodeInstall(getenv func(string) string) (string, []File, error) {
	config := claudeCodeUserDir(getenv)
	if config == "" {
		return "", nil, errors.New("can't tell where Claude Code keeps your Skills: neither CLAUDE_CONFIG_DIR nor HOME is set")
	}
	fm := claudeCodeFrontmatter{Name: jigflowInit, Description: jigflowInitDescription, ModelOff: true}
	content := installedHeader + " claude-code, which replaces it with the one the jfl running it ships.\n" +
		strings.TrimPrefix(skillMD(fm, jigflowInitPrompt), "---\n")
	return filepath.Join(config, "skills"), []File{{Path: jigflowInit + "/SKILL.md", Content: content}}, nil
}
