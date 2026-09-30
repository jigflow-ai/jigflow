package main_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"go.yaml.in/yaml/v3"
)

// skillFrontmatter parses the YAML frontmatter a published SKILL.md starts
// with, and returns it with the Markdown after it.
func skillFrontmatter(t *testing.T, content string) (map[string]any, string) {
	t.Helper()
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		t.Fatalf("SKILL.md does not start with a frontmatter block:\n%s", content)
	}
	fm, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("SKILL.md has no closing frontmatter delimiter:\n%s", content)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
		t.Fatalf("frontmatter is not YAML: %v\n%s", err, fm)
	}
	return m, body
}

// published is the ticket Playbook with the implement Skill described.
func published(t *testing.T) *clitest.Project {
	t.Helper()
	p := ticketPlaybook(t)
	p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\ndescription: Implement the Ticket in Focus with TDD.\n---\nWork on the Ticket in Focus, then move it on with `jfl move`.\n")
	return p
}

func TestTheClaudeCodeAdapterPublishesEachSkillAsAClaudeCodeSkill(t *testing.T) {
	p := published(t)
	p.MustRun("publish", "claude-code")

	fm, body := skillFrontmatter(t, p.Read(".claude/skills/implement/SKILL.md"))
	if desc, _ := fm["description"].(string); fm["name"] != "implement" || !strings.HasPrefix(desc, "Implement the Ticket in Focus with TDD.") {
		t.Errorf("frontmatter = %v, want the Skill's name and description", fm)
	}
	if !strings.Contains(body, "Work on the Ticket in Focus, then move it on with `jfl move`.\n") {
		t.Errorf("body =\n%s\nwant the Skill's prompt", body)
	}
	for _, field := range []string{"changes", "invocation"} {
		if _, ok := fm[field]; ok {
			t.Errorf("frontmatter = %v, want no JigFlow-only field %q", fm, field)
		}
	}
}

func TestTheClaudeCodeAdapterTranslatesInvocationModes(t *testing.T) {
	p := published(t)
	p.Write(".jigflow/skills/grill/SKILL.md", "---\nchanges: false\ninvocation: user\ndescription: Grill the user about a plan.\n---\nAsk one question at a time.\n")
	p.Write(".jigflow/skills/tdd/SKILL.md", "---\nchanges: false\ninvocation: agent\ndescription: Test-driven development.\n---\nRed, then green.\n")
	p.MustRun("publish", "claude-code")

	// A user Skill is invocable only by a person, through its slash command.
	grill, _ := skillFrontmatter(t, p.Read(".claude/skills/grill/SKILL.md"))
	if grill["disable-model-invocation"] != true {
		t.Errorf("user Skill frontmatter = %v, want disable-model-invocation: true", grill)
	}
	// An agent Skill is invocable by the model whenever relevant, and so is
	// a bound one, which the model runs when jfl next names it.
	for _, name := range []string{"tdd", "implement"} {
		fm, _ := skillFrontmatter(t, p.Read(".claude/skills/"+name+"/SKILL.md"))
		for _, field := range []string{"disable-model-invocation", "user-invocable"} {
			if _, ok := fm[field]; ok {
				t.Errorf("%s frontmatter = %v, want no %s: the model may invoke it", name, fm, field)
			}
		}
	}
	implement, _ := skillFrontmatter(t, p.Read(".claude/skills/implement/SKILL.md"))
	want := "Implement the Ticket in Focus with TDD. Run it when jfl next names it, on the Artifact it names: a Ticket in ready-for-agent or in-progress."
	if implement["description"] != want {
		t.Errorf("bound Skill description = %q, want %q", implement["description"], want)
	}
}

func TestGuidelinesArePublishedNextToTheSkillsThatNameThemAndLoadedOnlyWhenNeeded(t *testing.T) {
	p := published(t)
	p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\nguidelines: [go-style]\n---\nWork on the Ticket in Focus.\n")
	p.Write(".jigflow/guidelines/go-style.md", "Run gofmt before every commit.\n")
	p.Write(".jigflow/guidelines/php-style.md", "Follow PSR-12.\n")
	p.MustRun("publish", "claude-code")

	if got := p.Read(".claude/skills/implement/guidelines/go-style.md"); got != "Run gofmt before every commit.\n" {
		t.Errorf("published Guideline = %q, want the Guideline's rules", got)
	}
	_, body := skillFrontmatter(t, p.Read(".claude/skills/implement/SKILL.md"))
	if !strings.Contains(body, "[go-style](guidelines/go-style.md)") {
		t.Errorf("body =\n%s\nwant a link to the go-style Guideline", body)
	}
	// The Skill links its Guideline rather than holding it, so the agent
	// reads it only when it needs it; Guidelines no Skill names aren't
	// published at all.
	if strings.Contains(body, "Run gofmt") {
		t.Errorf("body =\n%s\nwant the Guideline linked, not inlined", body)
	}
	if _, ok := tree(t, p)[".claude/skills/implement/guidelines/php-style.md"]; ok {
		t.Errorf("php-style, which no Skill names, was published")
	}
}

// withBindings replaces the ticket Playbook's Bindings with the given
// YAML, indented under bindings:.
func withBindings(p *clitest.Project, bindings string) {
	ticket := p.Read(".jigflow/types/ticket.yaml")
	start := strings.Index(ticket, "bindings:\n")
	end := strings.Index(ticket, "transitions:\n")
	p.Write(".jigflow/types/ticket.yaml", ticket[:start]+"bindings:\n"+bindings+ticket[end:])
}

func TestABindingMayAskForAFreshSessionOrAnIsolatedSubAgent(t *testing.T) {
	p := published(t)
	withBindings(p, "  ready-for-agent: {skill: implement, fresh: true, isolated: true}\n  in-progress: {skill: implement, isolated: true}\n")
	p.MustRun("create", "Ticket", "--title", "Login")
	if r := p.MustRun("next"); !strings.Contains(r.Stdout, "run /implement on T-1") {
		t.Errorf("next = %q, want the Binding's Skill", r.Stdout)
	}

	withBindings(p, "  ready-for-agent: {skill: implement, frsh: true}\n")
	if r := p.Run("check"); r.ExitCode == 0 || !strings.Contains(r.Stderr, "ticket.yaml") || !strings.Contains(r.Stderr, "frsh") {
		t.Errorf("a misspelt Binding field: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

func TestTheClaudeCodeAdapterRunsASkillEveryBindingIsolatesInAForkedSubAgent(t *testing.T) {
	p := published(t)
	withBindings(p, "  ready-for-agent: {skill: implement, isolated: true}\n  in-progress: {skill: implement, isolated: true}\n")
	p.MustRun("publish", "claude-code")

	fm, body := skillFrontmatter(t, p.Read(".claude/skills/implement/SKILL.md"))
	if fm["context"] != "fork" {
		t.Errorf("frontmatter = %v, want context: fork", fm)
	}
	if strings.Contains(body, "sub-agent") {
		t.Errorf("body =\n%s\nwant no advice: Claude Code isolates the Skill itself", body)
	}
}

func TestBindingHintsClaudeCodeCannotApplyAreShownAsAdvice(t *testing.T) {
	p := published(t)
	// Claude Code can't start a fresh session for a Skill, nor fork only
	// some of the runs of one.
	withBindings(p, "  ready-for-agent: {skill: implement, fresh: true, isolated: true}\n  in-progress: implement\n")
	p.MustRun("publish", "claude-code")

	fm, body := skillFrontmatter(t, p.Read(".claude/skills/implement/SKILL.md"))
	if _, ok := fm["context"]; ok {
		t.Errorf("frontmatter = %v, want no context: only one Binding asks for a sub-agent", fm)
	}
	for _, want := range []string{
		"On a Ticket in ready-for-agent, run this Skill in a fresh session: start a new session (or /clear) before you run it.",
		"On a Ticket in ready-for-agent, run this Skill in an isolated sub-agent.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body =\n%s\nwant the advice %q", body, want)
		}
	}
}

func TestARouterSkillIsGeneratedFromThePlaybooksBindings(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write(".jigflow/types/2-ticket.yaml", strings.Replace(p.Read(".jigflow/types/2-ticket.yaml"), "in-progress: implement", "in-progress: {skill: implement, fresh: true}", 1))
	p.MustRun("publish", "claude-code")

	fm, body := skillFrontmatter(t, p.Read(".claude/skills/jigflow/SKILL.md"))
	if fm["name"] != "jigflow" || fm["description"] == nil {
		t.Errorf("router frontmatter = %v, want name jigflow and a description", fm)
	}
	for _, want := range []string{
		"Run `jfl next`",
		"## Spec\n\n- ready-for-agent: /to-tickets\n- ticketed: final\n",
		"## Ticket\n\n- ready-for-agent: /implement\n- in-progress: /implement, in a fresh session\n- in-review: /code-review\n- ready-to-merge: human work, never an agent's\n- done: final\n",
		"- needs-info: human work, never an agent's\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("router body =\n%s\nwant %q", body, want)
		}
	}
}

func TestPublishingRefusesASkillNamedLikeTheRouter(t *testing.T) {
	p := published(t)
	writeSkill(p, "jigflow", false)
	r := p.Run("publish", "claude-code")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, `Skill "jigflow"`) {
		t.Errorf("publish: exit %d, stderr %q; want the router's name refused", r.ExitCode, r.Stderr)
	}
}

func TestRePublishingIsIdempotent(t *testing.T) {
	p := published(t)
	p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\nguidelines: [go-style]\n---\nWork on the Ticket in Focus.\n")
	p.Write(".jigflow/guidelines/go-style.md", "Run gofmt.\n")
	first := p.MustRun("publish", "claude-code")
	if !strings.Contains(first.Stdout, "wrote .claude/skills/implement/SKILL.md") {
		t.Errorf("first publish = %q, want the files it wrote", first.Stdout)
	}
	before := tree(t, p)

	again := p.MustRun("publish", "claude-code")
	if want := "published Playbook \"skeleton\" for Claude Code: nothing changed\n"; again.Stdout != want {
		t.Errorf("publishing again = %q, want %q", again.Stdout, want)
	}
	if after := tree(t, p); !maps.Equal(before, after) {
		t.Errorf("publishing again changed the project:\nbefore %v\nafter  %v", before, after)
	}
}

func TestRePublishingRemovesWhatIsNoLongerInThePlaybookButNotWhatAPersonWrote(t *testing.T) {
	p := published(t)
	p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\nguidelines: [go-style]\n---\nWork on the Ticket in Focus.\n")
	p.Write(".jigflow/guidelines/go-style.md", "Run gofmt.\n")
	writeSkill(p, "triage", false)
	p.Write(".claude/skills/mine/SKILL.md", "---\nname: mine\n---\nMy own Skill.\n")
	p.Write(".claude/commands/mine.md", "My own command.\n")
	p.MustRun("publish", "claude-code")

	if err := os.RemoveAll(filepath.Join(p.Dir, ".jigflow/skills/triage")); err != nil {
		t.Fatal(err)
	}
	p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\n---\nWork on the Ticket in Focus.\n")
	r := p.MustRun("publish", "claude-code")
	for _, want := range []string{"removed .claude/skills/triage/SKILL.md", "removed .claude/skills/implement/guidelines/go-style.md"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("publish = %q, want %q", r.Stdout, want)
		}
	}

	for _, gone := range []string{".claude/skills/triage", ".claude/skills/implement/guidelines"} {
		if _, err := os.Stat(filepath.Join(p.Dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there after re-publishing (err %v)", gone, err)
		}
	}
	if p.Read(".claude/skills/mine/SKILL.md") != "---\nname: mine\n---\nMy own Skill.\n" || p.Read(".claude/commands/mine.md") != "My own command.\n" {
		t.Errorf("re-publishing touched files a person wrote")
	}
}

func TestPublishingNeverOverwritesASkillAPersonWrote(t *testing.T) {
	p := published(t)
	const mine = "---\nname: implement\n---\nMy own way to implement.\n"
	p.Write(".claude/skills/implement/SKILL.md", mine)

	r := p.Run("publish", "claude-code")
	if want := ".claude/skills/implement/SKILL.md was not published by jfl"; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("publish: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
	if p.Read(".claude/skills/implement/SKILL.md") != mine {
		t.Errorf("publish overwrote the Skill a person wrote")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".claude/skills/jigflow")); !os.IsNotExist(err) {
		t.Errorf("a refused publish still wrote the router (err %v)", err)
	}
}

func TestTheAgentsMdFallbackDescribesThePlaybookTheJflCommandsAndTheRules(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write(".jigflow/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\ndescription: Implement the Ticket in Focus.\n---\nWork on the Ticket in Focus.\n")
	p.MustRun("publish", "agents-md")

	agents := p.Read("AGENTS.md")
	for _, want := range []string{
		// The Playbook: its Artifact Types, and what works on each Status.
		`JigFlow Playbook "pocock"`,
		"#### Ticket\n\n- ready-for-agent: /implement\n- in-progress: /implement\n- in-review: /code-review\n- ready-to-merge: human work, never an agent's\n- done: final\n",
		// The jfl commands.
		"`jfl next`", "`jfl move <id> <status>`", "`jfl propose <file>`", "`jfl create <Type> --title <title>`",
		// The rules.
		"JFL_SESSION",
		"Never edit an Artifact's frontmatter",
		"Human Transition",
		"Only a person may approve",
		// The Skills, and where to read them.
		"- /implement: Implement the Ticket in Focus. Read `.agents/skills/implement/SKILL.md`",
	} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md =\n%s\nwant %q", agents, want)
		}
	}
	fm, body := skillFrontmatter(t, p.Read(".agents/skills/implement/SKILL.md"))
	if fm["name"] != "implement" || body != "Work on the Ticket in Focus.\n" {
		t.Errorf("published Skill = %v\n%s", fm, body)
	}
}

func TestTheAgentsMdFallbackShowsInvocationModesAndBindingHintsAsAdvice(t *testing.T) {
	p := published(t)
	withBindings(p, "  ready-for-agent: {skill: implement, isolated: true}\n  in-progress: {skill: implement, isolated: true}\n")
	p.Write(".jigflow/skills/grill/SKILL.md", "---\nchanges: false\ninvocation: user\n---\nAsk one question at a time.\n")
	p.MustRun("publish", "agents-md")

	if _, body := skillFrontmatter(t, p.Read(".agents/skills/implement/SKILL.md")); !strings.Contains(body, "On a Ticket in ready-for-agent or in-progress, run this Skill in an isolated sub-agent.") {
		t.Errorf("body =\n%s\nwant the sub-agent as advice", body)
	}
	if !strings.Contains(p.Read("AGENTS.md"), "- /grill: only when a person asks for it by name.") {
		t.Errorf("AGENTS.md =\n%s\nwant grill to be a person's to start", p.Read("AGENTS.md"))
	}
}

func TestTheAgentsMdFallbackKeepsWhatAPersonWroteInAGENTSmd(t *testing.T) {
	p := published(t)
	p.Write("AGENTS.md", "# My project\n\nUse tabs.\n")
	p.MustRun("publish", "agents-md")
	first := p.Read("AGENTS.md")
	if !strings.HasPrefix(first, "# My project\n\nUse tabs.\n") || !strings.Contains(first, "jfl next") {
		t.Errorf("AGENTS.md =\n%s\nwant the person's text, then the Playbook", first)
	}

	p.Write("AGENTS.md", strings.Replace(first, "Use tabs.", "Use spaces.", 1))
	if r := p.MustRun("publish", "agents-md"); !strings.Contains(r.Stdout, ": nothing changed\n") {
		t.Errorf("publishing again = %q, want nothing changed", r.Stdout)
	}
	if got := p.Read("AGENTS.md"); got != strings.Replace(first, "Use tabs.", "Use spaces.", 1) {
		t.Errorf("AGENTS.md =\n%s\nwant it unchanged but for the person's edit", got)
	}
}

func TestPublishingRemovesOnlyFilesWhereItsAdapterPublishes(t *testing.T) {
	p := published(t)
	p.Write("notes.md", "Mine.\n")
	p.Write(".jigflow/published.yaml", "claude-code:\n  - notes.md\n")

	r := p.Run("publish", "claude-code")
	if want := `lists notes.md, which the claude-code Adapter doesn't publish`; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("publish: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
	if p.Read("notes.md") != "Mine.\n" {
		t.Errorf("publish removed a file outside .claude/skills")
	}
}

// hookCommands returns, per hook event, the commands the hooks in a Claude
// Code settings file run.
func hookCommands(t *testing.T, settings string) map[string][]string {
	t.Helper()
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil {
		t.Fatalf(".claude/settings.json isn't JSON: %v\n%s", err, settings)
	}
	out := map[string][]string{}
	for event, groups := range s.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[event] = append(out[event], h.Type+": "+h.Command)
			}
		}
	}
	return out
}

func TestTheClaudeCodeAdapterSetsUpTheHooksThatReadTokenUsageFromClaudeCodesRecords(t *testing.T) {
	p := published(t)
	p.MustRun("publish", "claude-code")

	got := hookCommands(t, p.Read(".claude/settings.json"))
	for _, event := range []string{"SessionStart", "Stop", "SubagentStop", "SessionEnd"} {
		if !slices.Equal(got[event], []string{"command: jfl hook claude-code"}) {
			t.Errorf("%s hooks = %q, want jfl hook claude-code", event, got[event])
		}
	}
}

func TestPublishingKeepsThePersonsOwnClaudeCodeSettingsAndHooks(t *testing.T) {
	p := published(t)
	p.Write(".claude/settings.json", `{
  "permissions": {"allow": ["Bash(make test)"]},
  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]}]}
}
`)
	p.MustRun("publish", "claude-code")
	p.MustRun("publish", "claude-code")

	settings := p.Read(".claude/settings.json")
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil || !slices.Equal(s.Permissions.Allow, []string{"Bash(make test)"}) {
		t.Errorf("publishing lost the person's permissions (%v):\n%s", err, settings)
	}
	if got := hookCommands(t, settings)["Stop"]; !slices.Equal(got, []string{"command: notify-send done", "command: jfl hook claude-code"}) {
		t.Errorf("Stop hooks = %q, want the person's and jfl's, once", got)
	}
}

// The router stands in for a Playbook's own "which skill do I use?" Skill
// (ask-matt, in the Pocock Playbook): a Skill only a person starts has no
// description the agent sees, so the router names it for the person.
func TestTheRouterNamesTheSkillsOnlyAPersonStarts(t *testing.T) {
	p := published(t)
	p.Write(".jigflow/skills/wayfinder/SKILL.md", "---\nchanges: true\ninvocation: user\ndescription: Plan a huge chunk of work as a shared map of decision tickets.\n---\nChart the map.\n")
	p.Write(".jigflow/skills/grilling/SKILL.md", "---\nchanges: false\ninvocation: agent\ndescription: Grill the user about a plan.\n---\nAsk.\n")
	p.MustRun("publish", "claude-code")

	_, body := skillFrontmatter(t, p.Read(".claude/skills/jigflow/SKILL.md"))
	if want := "\n## Skills only a person starts\n\nWhen a person asks which Skill fits what they want to do, point them to one of these, which only they can start, by typing its name:\n\n- /wayfinder: Plan a huge chunk of work as a shared map of decision tickets.\n"; !strings.Contains(body, want) {
		t.Errorf("router body =\n%s\nwant %q", body, want)
	}
	if strings.Contains(body, "/grilling") {
		t.Errorf("router body =\n%s\nwant no agent-invoked Skill among those a person starts", body)
	}
}

// assertAsksThroughJflsTools fails unless the published text tells the agent
// to ask the person for a Confirmation through jfl's MCP tools, naming each
// of tools, and otherwise to tell them what waits for them and where.
func assertAsksThroughJflsTools(t *testing.T, what, content string, tools ...string) {
	t.Helper()
	wants := []string{"jfl's MCP tools", "a form only they see", "in their own terminal, or the Dashboard (`jfl ui`)"}
	for _, tool := range tools {
		wants = append(wants, "the `"+tool+"` tool")
	}
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Errorf("%s should tell the agent to ask the person through jfl's tools, containing %q:\n%s", what, want, content)
		}
	}
}

func TestTheRouterAndAGENTSmdTellTheAgentToAskThePersonThroughJflsTools(t *testing.T) {
	p := published(t)
	p.MustRun("publish", "claude-code")
	p.MustRun("publish", "agents-md")

	assertAsksThroughJflsTools(t, "the router Skill", p.Read(".claude/skills/jigflow/SKILL.md"), "propose", "approve", "move")
	agents := p.Read("AGENTS.md")
	assertAsksThroughJflsTools(t, "AGENTS.md", agents, "propose", "approve", "move")
	if strings.Contains(agents, "propose it, never try to make it") {
		t.Errorf("AGENTS.md should no longer say only to propose a Human Transition:\n%s", agents)
	}
}

// mcpServers returns the servers a project's .mcp.json registers, by name.
func mcpServers(t *testing.T, content string) map[string]map[string]any {
	t.Helper()
	var m struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		t.Fatalf(".mcp.json isn't JSON: %v\n%s", err, content)
	}
	return m.Servers
}

func TestTheClaudeCodeAdapterRegistersJflsMCPServerInTheProject(t *testing.T) {
	p := published(t)
	r := p.MustRun("publish", "claude-code")
	if !strings.Contains(r.Stdout, "wrote .mcp.json") {
		t.Errorf("publish = %q, want it to say it wrote .mcp.json", r.Stdout)
	}

	jfl := mcpServers(t, p.Read(".mcp.json"))["jfl"]
	if jfl["type"] != "stdio" || jfl["command"] != "jfl" || !slices.Equal(anyStrings(jfl["args"]), []string{"mcp"}) {
		t.Errorf(".mcp.json jfl server = %v, want jfl mcp over stdio", jfl)
	}
}

// anyStrings is a JSON list of strings as a []string.
func anyStrings(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, s := range l {
		str, _ := s.(string)
		out = append(out, str)
	}
	return out
}

func TestPublishingReplacesOnlyJflsServerInMcpJson(t *testing.T) {
	p := published(t)
	p.Write(".mcp.json", `{
  "mcpServers": {
    "github": {"command": "gh-mcp", "args": ["--port", "8080"], "env": {"TOKEN": "x"}},
    "jfl": {"command": "/old/jfl", "args": ["serve"]}
  },
  "note": 42
}
`)
	p.MustRun("publish", "claude-code")
	if r := p.MustRun("publish", "claude-code"); !strings.HasSuffix(r.Stdout, "nothing changed\n") {
		t.Errorf("publishing again = %q, want nothing changed", r.Stdout)
	}

	content := p.Read(".mcp.json")
	servers := mcpServers(t, content)
	if jfl := servers["jfl"]; jfl["command"] != "jfl" || !slices.Equal(anyStrings(jfl["args"]), []string{"mcp"}) {
		t.Errorf("jfl server = %v, want it replaced by jfl mcp", jfl)
	}
	gh := servers["github"]
	if env, _ := gh["env"].(map[string]any); gh["command"] != "gh-mcp" || !slices.Equal(anyStrings(gh["args"]), []string{"--port", "8080"}) || env["TOKEN"] != "x" {
		t.Errorf("github server = %v, want it as the person wrote it", gh)
	}
	if !strings.Contains(content, `"note": 42`) {
		t.Errorf(".mcp.json =\n%s\nwant the person's other keys kept", content)
	}
}

// Codex, Cursor and the other agents that read AGENTS.md keep their MCP
// servers in the user's own configuration, which a publish never writes.
func TestTheAgentsMdAdapterPrintsTheCommandThatRegistersJflsMCPServer(t *testing.T) {
	p := published(t)
	home := t.TempDir()
	p.Setenv("HOME", home)

	r := p.MustRun("publish", "agents-md")
	if !strings.Contains(r.Stdout, "codex mcp add jfl -- jfl mcp") {
		t.Errorf("publish = %q, want the command that registers jfl mcp", r.Stdout)
	}
	if again := p.MustRun("publish", "agents-md"); !strings.Contains(again.Stdout, "codex mcp add jfl -- jfl mcp") {
		t.Errorf("publishing again = %q, want the command still, since jfl can't tell it was run", again.Stdout)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf("agents-md wrote .mcp.json (err %v)", err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("agents-md wrote into the user's home: %v (err %v)", entries, err)
	}
}

func TestPublishRemoveDeletesWhatThatAdapterPublishedAndNothingElse(t *testing.T) {
	p := published(t)
	p.Write(".claude/skills/mine/SKILL.md", "---\nname: mine\n---\nMine.\n")
	p.Write(".claude/settings.json", `{"permissions": {"allow": ["Bash(make test)"]}}`+"\n")
	p.Write(".mcp.json", `{"mcpServers": {"db": {"command": "db-mcp"}}}`+"\n")
	p.MustRun("publish", "claude-code")
	p.MustRun("publish", "agents-md")
	agents := p.Read("AGENTS.md")

	r := p.MustRun("publish", "--remove", "claude-code")
	for _, want := range []string{"stopped publishing the Playbook for Claude Code", "  removed .claude/skills/implement/SKILL.md", "  removed .mcp.json"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("publish --remove should say %q:\n%s", want, r.Stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".claude/skills/implement")); !os.IsNotExist(err) {
		t.Errorf("the implement Skill Claude Code was published is still there")
	}
	if got := p.Read(".claude/skills/mine/SKILL.md"); got != "---\nname: mine\n---\nMine.\n" {
		t.Errorf("a Skill a person wrote was changed:\n%s", got)
	}
	if got := hookCommands(t, p.Read(".claude/settings.json")); len(got) != 0 {
		t.Errorf("jfl's hooks are still in .claude/settings.json: %v", got)
	}
	if !strings.Contains(p.Read(".claude/settings.json"), "Bash(make test)") {
		t.Errorf("the person's settings were not kept:\n%s", p.Read(".claude/settings.json"))
	}
	if servers := mcpServers(t, p.Read(".mcp.json")); servers["jfl"] != nil || servers["db"] == nil {
		t.Errorf(".mcp.json servers = %v, want the person's db and not jfl", servers)
	}
	if p.Read("AGENTS.md") != agents || !strings.Contains(p.Read(".agents/skills/implement/SKILL.md"), "Work on the Ticket in Focus") {
		t.Errorf("removing claude-code changed what agents-md published")
	}
	if manifest := p.Read(".jigflow/published.yaml"); strings.Contains(manifest, "claude-code") || !strings.Contains(manifest, "agents-md:") {
		t.Errorf("the Manifest should list agents-md alone:\n%s", manifest)
	}
}

func TestPublishRemoveSaysSoWhenNothingIsPublishedForTheAdapter(t *testing.T) {
	p := published(t)
	p.MustRun("publish", "agents-md")
	before := tree(t, p)

	r := p.MustRun("publish", "--remove", "claude-code")
	if want := "nothing is published for Claude Code: nothing changed\n"; r.Stdout != want {
		t.Errorf("publish --remove = %q, want %q", r.Stdout, want)
	}
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("removing what was never published changed the project")
	}
}

func TestInitNoLongerRepublishesThroughAnAdapterPublishRemoveStopped(t *testing.T) {
	p := initialised(t)
	p.MustRun("init", "--adapter", "claude-code")
	p.MustRun("publish", "agents-md")
	p.MustRun("publish", "--remove", "claude-code")

	p.MustRun("init")
	if _, err := os.Stat(filepath.Join(p.Dir, ".claude")); !os.IsNotExist(err) {
		t.Errorf("init published through claude-code again: %v", tree(t, p))
	}
	if manifest := p.Read(".jigflow/published.yaml"); strings.Contains(manifest, "claude-code") || !strings.Contains(manifest, "agents-md:") {
		t.Errorf("the Manifest should list agents-md alone:\n%s", manifest)
	}
}
