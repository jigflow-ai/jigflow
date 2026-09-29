package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// withClaudeConfig gives p's commands a Claude Code configuration directory
// of the test's own, and returns it.
func withClaudeConfig(t *testing.T, p *clitest.Project) string {
	t.Helper()
	dir := t.TempDir()
	p.Setenv("CLAUDE_CONFIG_DIR", dir)
	return dir
}

// readFile is the content of the file at path, outside the project.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInstallClaudeCodeWritesTheJigflowInitSkillToTheUsersSkills(t *testing.T) {
	p := bin.NewProject(t)
	config := withClaudeConfig(t, p)
	skill := filepath.Join(config, "skills", "jigflow-init", "SKILL.md")

	r := p.MustRun("install", "claude-code")
	if !strings.Contains(r.Stdout, "wrote "+skill) {
		t.Errorf("install should report the Skill it wrote, %s:\n%s", skill, r.Stdout)
	}
	fm, _ := skillFrontmatter(t, readFile(t, skill))
	if fm["name"] != "jigflow-init" || fm["disable-model-invocation"] != true {
		t.Errorf("jigflow-init should be a Skill only a person starts: %v", fm)
	}
	if files := tree(t, p); len(files) != 0 {
		t.Errorf("install wrote into the directory it ran in: %v", files)
	}
}

func TestInstallingAgainSaysNothingChangedAndUpdatesAnOlderSkill(t *testing.T) {
	p := bin.NewProject(t)
	config := withClaudeConfig(t, p)
	skill := filepath.Join(config, "skills", "jigflow-init", "SKILL.md")
	p.MustRun("install", "claude-code")
	installed := readFile(t, skill)

	if r := p.MustRun("install", "claude-code"); r.Stdout != "installed jfl's Skills for Claude Code: nothing changed\n" {
		t.Errorf("installing again = %q, want nothing changed", r.Stdout)
	}

	// The Skill an older jfl installed, whose text this one changed.
	_, body := skillFrontmatter(t, installed)
	if err := os.WriteFile(skill, []byte(strings.Replace(installed, body, "Set the project up.\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := p.MustRun("install", "claude-code"); !strings.Contains(r.Stdout, "wrote "+skill) {
		t.Errorf("installing over an older Skill should rewrite it:\n%s", r.Stdout)
	}
	if got := readFile(t, skill); got != installed {
		t.Errorf("the rewritten Skill =\n%s\nwant\n%s", got, installed)
	}
}

func TestInstallKeepsAJigflowInitSkillAPersonWrote(t *testing.T) {
	p := bin.NewProject(t)
	config := withClaudeConfig(t, p)
	own := "---\nname: jigflow-init\n---\nMy own way to set a project up.\n"
	skill := filepath.Join(config, "skills", "jigflow-init", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	r := p.Run("install", "claude-code")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, skill+" was not installed by jfl, so it isn't replaced") {
		t.Errorf("install over a person's Skill exited %d, want 1 saying it isn't replaced:\n%s", r.ExitCode, r.Stderr)
	}
	if got := readFile(t, skill); got != own {
		t.Errorf("install changed a Skill a person wrote:\n%s", got)
	}
}

func TestInstallClaudeCodeWithoutCLAUDE_CONFIG_DIRWritesUnderTheHomeDirectory(t *testing.T) {
	p := bin.NewProject(t)
	home := t.TempDir()
	p.Setenv("CLAUDE_CONFIG_DIR", "")
	p.Setenv("HOME", home)

	p.MustRun("install", "claude-code")
	fm, _ := skillFrontmatter(t, readFile(t, filepath.Join(home, ".claude", "skills", "jigflow-init", "SKILL.md")))
	if fm["name"] != "jigflow-init" {
		t.Errorf("the Skill installed under the home directory is %v", fm)
	}
}

func TestInstallRefusesAnAdapterWithNoPlaceForAUsersSkills(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		code int
		want string
	}{
		"agents-md":          {[]string{"agents-md"}, 1, "agents that read AGENTS.md have no documented place for a user's own Skills"},
		"an unknown Adapter": {[]string{"cursor"}, 1, `unknown Adapter "cursor" (want claude-code)`},
		"no Adapter":         {nil, 2, "jfl install <adapter> (claude-code)"},
	} {
		t.Run(name, func(t *testing.T) {
			p := bin.NewProject(t)
			config := withClaudeConfig(t, p)
			r := p.Run(append([]string{"install"}, c.args...)...)
			if r.ExitCode != c.code || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("install %v exited %d, want %d saying %q:\n%s", c.args, r.ExitCode, c.code, c.want, r.Stderr)
			}
			if entries, _ := os.ReadDir(config); len(entries) != 0 || len(tree(t, p)) != 0 {
				t.Errorf("a refused install wrote %v", entries)
			}
		})
	}
}

// jigflow-init tells the agent how to set the project up from chat with
// jfl init (ADR 0026). The interview itself is prose and isn't tested.
func TestTheJigflowInitSkillRunsInitAsAnAgentSessionWithEveryAnswerAsAFlag(t *testing.T) {
	p := bin.NewProject(t)
	config := withClaudeConfig(t, p)
	p.MustRun("install", "claude-code")
	_, skill := skillFrontmatter(t, readFile(t, filepath.Join(config, "skills", "jigflow-init", "SKILL.md")))

	for _, want := range []string{
		// It looks before it asks, and explains the Playbooks.
		"Look at the repository",
		"Larapilot-style", "Pocock", "Build your own",
		"`--adapter claude-code`",
		"git remote",
		// It runs init as an agent session, with every answer as a flag.
		"JFL_SESSION=",
		"jfl init --playbook",
		"--setting", "--label",
		"exit status 2",
		// It reads back what was set up and what waits for the person.
		"Read back",
		"never approve it",
		// It asks for a new session, and after it uses the approve tool.
		"new session",
		"not a resumed one",
		"the `approve` tool",
		"/playbook-author",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("jigflow-init should contain %q:\n%s", want, skill)
		}
	}
}
