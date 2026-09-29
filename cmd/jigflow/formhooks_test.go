package main_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// formHook is Claude Code settings with one hook at event, whose matcher
// is matcher, running command.
func formHook(event, matcher, command string) string {
	return fmt.Sprintf(`{"hooks": {%q: [{"matcher": %q, "hooks": [{"type": "command", "command": %q}]}]}}`, event, matcher, command)
}

// warnings is the lines of what jfl printed on stderr that warn.
func warnings(r clitest.Result) []string {
	var w []string
	for _, l := range strings.Split(r.Stderr, "\n") {
		if strings.HasPrefix(l, "jfl: warning: ") {
			w = append(w, l)
		}
	}
	return w
}

// wantOneWarning fails the test unless r succeeded and printed exactly one
// warning, which says each of want.
func wantOneWarning(t *testing.T, r clitest.Result, want ...string) {
	t.Helper()
	if r.ExitCode != 0 {
		t.Fatalf("exited %d, want 0 with a warning\nstderr: %s", r.ExitCode, r.Stderr)
	}
	w := warnings(r)
	if len(w) != 1 {
		t.Fatalf("warnings = %q, want one", w)
	}
	for _, s := range want {
		if !strings.Contains(w[0], s) {
			t.Errorf("the warning should say %q:\n%s", s, w[0])
		}
	}
}

// userHome gives the project's commands a user whose home is a fresh
// directory, where Claude Code keeps its settings under .claude, and
// returns it.
func userHome(t *testing.T, p *clitest.Project) string {
	t.Helper()
	home := t.TempDir()
	p.Setenv("HOME", home)
	p.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func TestCheckWarnsAboutAnElicitationHookInTheProjectThatCanAnswerTheForm(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".claude/settings.json", formHook("Elicitation", "jfl", "auto-approve.sh"))

	r := p.Run("check")
	wantOneWarning(t, r, ".claude/settings.json", "Elicitation hook", `"jfl"`, "auto-approve.sh",
		"answer the Confirmation form", "approve Proposals", "make Human Transitions")
	if want := `Playbook "skeleton": no problems`; firstLine(r.Stdout) != want {
		t.Errorf("check = %q, want %q", firstLine(r.Stdout), want)
	}
}

func TestCheckWarnsAboutAnElicitationResultHookThatCanChangeThePersonsAnswer(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".claude/settings.local.json", formHook("ElicitationResult", "", "rewrite.sh"))

	wantOneWarning(t, p.Run("check"), ".claude/settings.local.json", "ElicitationResult hook", "rewrite.sh",
		"change the answer", "approve Proposals", "make Human Transitions")
}

func TestCheckWarnsAboutAFormHookInTheUsersSettings(t *testing.T) {
	p := ticketPlaybook(t)
	home := userHome(t, p)
	p.Write(".claude/settings.json", `{"permissions": {"allow": ["Bash(make test)"]}}`)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), formHook("Elicitation", "*", "answer-everything"))

	wantOneWarning(t, p.Run("check"), filepath.Join(home, ".claude", "settings.json"), "Elicitation hook", "answer-everything")
}

func TestCheckReadsTheUsersSettingsWhereClaudeConfigDirPutsThem(t *testing.T) {
	p := ticketPlaybook(t)
	config := t.TempDir()
	p.Setenv("CLAUDE_CONFIG_DIR", config)
	writeFile(t, filepath.Join(config, "settings.json"), formHook("Elicitation", "jfl", "answer.sh"))

	wantOneWarning(t, p.Run("check"), filepath.Join(config, "settings.json"), "answer.sh")
}

func TestFormHooksWhoseMatcherMatchesJflsServerAreWarnedAbout(t *testing.T) {
	for _, matcher := range []string{"jfl", "*", "", "github|jfl", "github, jfl", ".*", "^j", "fl$", "mcp|j.l"} {
		t.Run(matcher, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.Write(".claude/settings.json", formHook("Elicitation", matcher, "answer.sh"))
			wantOneWarning(t, p.Run("check"), "answer.sh")
		})
	}
}

func TestFormHooksWhoseMatcherDoesNotMatchJflsServerAreNotWarnedAbout(t *testing.T) {
	for _, matcher := range []string{"github", "jfl-extra", "my jfl", "github|linear", "^jf$", "linear.*"} {
		t.Run(matcher, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.Write(".claude/settings.json", formHook("Elicitation", matcher, "answer.sh"))
			r := p.MustRun("check")
			if w := warnings(r); len(w) != 0 {
				t.Errorf("warnings = %q, want none", w)
			}
		})
	}
}

func TestOtherHooksAreNotWarnedAbout(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".claude/settings.json", formHook("PreToolUse", "*", "lint.sh"))
	if w := warnings(p.MustRun("check")); len(w) != 0 {
		t.Errorf("warnings = %q, want none", w)
	}
}

func TestMissingUserSettingsGiveNoWarningAndNoError(t *testing.T) {
	p := ticketPlaybook(t)
	userHome(t, p)
	r := p.MustRun("check")
	if r.Stderr != "" {
		t.Errorf("stderr = %q, want nothing", r.Stderr)
	}

	p.Setenv("HOME", "")
	r = p.MustRun("check")
	if r.Stderr != "" {
		t.Errorf("with no HOME, stderr = %q, want nothing", r.Stderr)
	}
}

func TestSettingsThatDoNotParseAreWarnedAboutWithoutFailing(t *testing.T) {
	p := ticketPlaybook(t)
	home := userHome(t, p)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), "{not json")

	wantOneWarning(t, p.Run("check"), filepath.Join(home, ".claude", "settings.json"), "can't tell")
}

func TestPublishWarnsAboutAFormHookAndStillPublishes(t *testing.T) {
	p := published(t)
	p.Write(".claude/settings.json", formHook("ElicitationResult", "jfl", "rewrite.sh"))

	r := p.Run("publish", "claude-code")
	wantOneWarning(t, r, ".claude/settings.json", "ElicitationResult hook", "rewrite.sh")
	if !strings.Contains(p.Read(".mcp.json"), `"jfl"`) {
		t.Errorf("publish didn't register jfl's MCP server:\n%s", p.Read(".mcp.json"))
	}
}

func TestTheDashboardShowsTheWarningAboutAFormHook(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".claude/settings.json", formHook("Elicitation", "jfl", "auto-approve.sh"))
	ui := p.StartUI()

	page := get(t, ui, "/")
	wantText(t, section(t, page, "Hooks that can answer for you"),
		".claude/settings.json", "Elicitation hook", "auto-approve.sh", "answer the Confirmation form")
}

func TestTheDashboardShowsNoHookWarningWhenThereIsNone(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()

	if page := get(t, ui, "/"); strings.Contains(text(page), "Hooks that can answer for you") {
		t.Errorf("the backlog warns about hooks there are none of:\n%s", text(page))
	}
}

func TestCheckOfAProposalWarnsAboutAFormHookToo(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: own\n")
	p.Write("author.yaml", typeProposal("a Playbook for notes", "Note", noteType, map[string]string{"write-note": writeNote}))
	p.MustRun("propose", "author.yaml")
	p.Write(".claude/settings.json", formHook("Elicitation", "jfl", "auto-approve.sh"))

	wantOneWarning(t, p.Run("check", "--proposal", "P-1"), ".claude/settings.json", "auto-approve.sh")
}

func TestInitWarnsAboutAFormHookOnce(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".claude/settings.json", formHook("Elicitation", "jfl", "auto-approve.sh"))

	wantOneWarning(t, p.Run("init", "--playbook", "larapilot", "--adapter", "claude-code"), "auto-approve.sh")
}
