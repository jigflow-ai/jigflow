package main_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

func TestInitHasNoDefaultPlaybook(t *testing.T) {
	p := bin.NewProject(t)

	r := p.Run("init", "--adapter", "claude-code")
	if r.ExitCode != 2 {
		t.Fatalf("init without a Playbook choice or a terminal exited %d, want 2; stderr: %s", r.ExitCode, r.Stderr)
	}
	for _, want := range []string{"there is no default", "larapilot", "pocock", "own"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("refusal should contain %q:\n%s", want, r.Stderr)
		}
	}
	if files := tree(t, p); len(files) != 0 {
		t.Errorf("a refused init wrote %v", files)
	}
}

func TestInitAsksWhichPlaybookAndAdapterInATerminal(t *testing.T) {
	p := bin.NewProject(t)

	term := p.StartInTerminal("init")
	term.Expect("There is no default.")
	for _, want := range []string{"Larapilot-style", "Pocock", "Build my own"} {
		term.Expect(want)
	}
	term.Expect("Playbook [1-3]: ")
	term.Type("3\n")
	term.Expect("Adapter [1-2]: ")
	term.Type("1\n")
	r := term.Wait()
	if r.ExitCode != 0 {
		t.Fatalf("init exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}
	if !strings.Contains(r.Output, "Next, run the playbook-author Skill in Claude Code") {
		t.Errorf("Build my own should say to run the playbook-author Skill, which jfl ships:\n%s", r.Output)
	}
	p.MustRun("check")
	if fm, _ := skillFrontmatter(t, p.Read(".claude/skills/playbook-author/SKILL.md")); fm["disable-model-invocation"] != true {
		t.Errorf("playbook-author should be published as a Skill only a person starts: %v", fm)
	}
	if !strings.Contains(p.Read(".jigflow/published.yaml"), "claude-code:") {
		t.Errorf("init didn't publish through the chosen Adapter:\n%s", p.Read(".jigflow/published.yaml"))
	}
}

// initialised is a Go project whose Playbook declares the Gates tests and
// lint by name only, leaving their commands to the project.
func initialised(t *testing.T) *clitest.Project {
	t.Helper()
	p := gatedPlaybook(t, "      - name: tests\n      - name: lint\n", "")
	p.Write("go.mod", "module example.com/shop\n\ngo 1.26\n")
	p.Write(".editorconfig", "root = true\n")
	p.Write(".golangci.yml", "linters:\n  enable: [errcheck]\n")
	return p
}

func TestInitProposesGatesAndAStarterGuidelineForTheToolchainAsOneProposal(t *testing.T) {
	p := initialised(t)

	r := p.MustRun("init", "--adapter", "agents-md")
	for _, want := range []string{
		"Go (go.mod)",
		"proposed P-1",
		`1. give Gate "tests" the command go test ./...`,
		`2. give Gate "lint" the command go vet ./...`,
		`3. add Guideline "conventions"`,
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("init output should contain %q:\n%s", want, r.Stdout)
		}
	}
	if r := p.MustRun("simulate", "Ticket"); !strings.Contains(r.Stdout, "tests (no command yet)") {
		t.Errorf("a proposed Gate command shouldn't apply before a person approves it:\n%s", r.Stdout)
	}

	approveInTerminal(t, p, "P-1")
	if r := p.MustRun("simulate", "Ticket"); !strings.Contains(r.Stdout, "tests (go test ./...), lint (go vet ./...)") {
		t.Errorf("simulate after approving should show the Gates' commands:\n%s", r.Stdout)
	}
	guideline := p.Read(".jigflow/guidelines/conventions.md")
	for _, want := range []string{"Go", "go test ./...", ".editorconfig", ".golangci.yml"} {
		if !strings.Contains(guideline, want) {
			t.Errorf("the starter Guideline should mention %q:\n%s", want, guideline)
		}
	}
}

func TestInitDetectsTheToolchainFromTheProjectsFiles(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  []string // the Gates proposed, as their items read
	}{
		"Rust": {map[string]string{"Cargo.toml": "[package]\n"},
			[]string{`give Gate "tests" the command cargo test`, `give Gate "lint" the command cargo clippy -- -D warnings`}},
		"Python with Ruff": {map[string]string{"pyproject.toml": "[tool.ruff]\nline-length = 100\n"},
			[]string{`give Gate "tests" the command pytest`, `give Gate "lint" the command ruff check .`}},
		"Node.js with pnpm": {map[string]string{"package.json": `{"scripts": {"test": "vitest run", "lint": "eslint ."}}`, "pnpm-lock.yaml": ""},
			[]string{`give Gate "tests" the command pnpm test`, `give Gate "lint" the command pnpm run lint`}},
		"PHP with Pest and Pint": {map[string]string{"composer.json": `{"require-dev": {"pestphp/pest": "^3", "laravel/pint": "^1"}}`},
			[]string{`give Gate "tests" the command vendor/bin/pest`, `give Gate "lint" the command vendor/bin/pint --test`}},
		"a Makefile before the language's tools": {map[string]string{"Makefile": "test:\n\tgo test ./...\n", "go.mod": "module x\n"},
			[]string{`give Gate "tests" the command make test`, `give Gate "lint" the command go vet ./...`}},
	} {
		t.Run(name, func(t *testing.T) {
			p := gatedPlaybook(t, "      - name: tests\n", "")
			for f, content := range tc.files {
				p.Write(f, content)
			}
			r := p.MustRun("init", "--adapter", "agents-md")
			for _, want := range tc.want {
				if !strings.Contains(r.Stdout, want) {
					t.Errorf("init should propose %q:\n%s", want, r.Stdout)
				}
			}
		})
	}
}

func TestRunningInitAgainChangesNothingItAlreadySetUp(t *testing.T) {
	p := initialised(t)
	p.MustRun("init", "--adapter", "claude-code")
	before := tree(t, p)

	r := p.MustRun("init", "--playbook", "own")
	if !strings.Contains(r.Stdout, "keeping it") || !strings.Contains(r.Stdout, "nothing new to propose") || !strings.Contains(r.Stdout, "nothing changed") {
		t.Errorf("init again should keep the Playbook, propose nothing new while P-1 is pending and republish nothing:\n%s", r.Stdout)
	}
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("init again changed the project")
	}

	approveInTerminal(t, p, "P-1")
	p.Write(".jigflow/guidelines/conventions.md", "# Conventions\n\nOurs.\n")
	if r := p.MustRun("init"); !strings.Contains(r.Stdout, "nothing new to propose") {
		t.Errorf("init once P-1 is approved should propose nothing new:\n%s", r.Stdout)
	}
	if got := p.Read(".jigflow/guidelines/conventions.md"); got != "# Conventions\n\nOurs.\n" {
		t.Errorf("init again changed the person's Guideline:\n%s", got)
	}
}

func TestInitIsAPersons(t *testing.T) {
	p := bin.NewProject(t)
	r := p.RunInSession("A", "init", "--playbook", "own", "--adapter", "claude-code")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "a person's") {
		t.Errorf("an agent session's init exited %d: %s", r.ExitCode, r.Stderr)
	}
}

// trackerBase is a project extending a Base Playbook whose Tickets are kept
// in a tracker through the fake Connector, declared with its tracker file
// left for the project to name and none of the Ticket's Statuses mapped.
func trackerBase(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write("base/playbook.yaml", `name: base
connectors:
  tracker:
    command: `+fakeConnector+`
    settings: {tracker: "", log: calls.jsonl}
`)
	p.Write("base/types/ticket.yaml", strings.Replace(baseTicket, "prefix: T\n", "prefix: T\nstore: tracker\n", 1))
	writeSkillIn(p, "base", "implement", true)
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {path: base}\n")
	setTracker(t, p, map[string]any{"next": 41})
	return p
}

func TestInitSetsUpTheConnectorOfATrackerStoredTypeWithItsLabels(t *testing.T) {
	p := trackerBase(t)

	r := p.MustRun("init", "--adapter", "agents-md", "--setting", "tracker.tracker=tracker.json", "--label", "Ticket.in-progress=status: doing")
	if !strings.Contains(r.Stdout, `Connector "tracker"`) {
		t.Errorf("init should report setting up the Connector:\n%s", r.Stdout)
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-41", "in-progress")
	if i := item(t, p, "41"); fmt.Sprint(i.Labels) != "[status: doing]" {
		t.Errorf("T-41 in progress is labelled %v, want the label init mapped", i.Labels)
	}
	pbFile := p.Read(".jigflow/playbook.yaml")
	for _, want := range []string{"extends: {path: base}", "ready-for-agent: ready-for-agent", "done: done"} {
		if !strings.Contains(pbFile, want) {
			t.Errorf("the Playbook file should map every Status, keeping what it said, with %q:\n%s", want, pbFile)
		}
	}

	before := tree(t, p)
	p.MustRun("init")
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("init again changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
}

func TestInitAsksForTheConnectorsSettingsAndLabelsInATerminal(t *testing.T) {
	p := trackerBase(t)

	term := p.StartInTerminal("init", "--adapter", "agents-md")
	term.Expect(`Connector "tracker": tracker: `)
	term.Type("tracker.json\n")
	term.Expect(`Ticket in "ready-for-agent" is labelled [ready-for-agent]: `)
	term.Type("\n")
	term.Expect(`Ticket in "in-progress" is labelled [in-progress]: `)
	term.Type("status: doing\n")
	term.Expect(`Ticket in "done" is labelled [done]: `)
	term.Type("\n")
	if r := term.Wait(); r.ExitCode != 0 {
		t.Fatalf("init exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-41", "in-progress")
	if i := item(t, p, "41"); fmt.Sprint(i.Labels) != "[status: doing]" {
		t.Errorf("T-41 in progress is labelled %v, want the label the person gave", i.Labels)
	}
}

func TestAProposalsChangesToThePlaybookApplyAllOrNothing(t *testing.T) {
	p := gatedPlaybook(t, "      - name: tests\n", "")
	p.Write("setup.yaml", "summary: check the work\nitems:\n  - {gate: tests, cmd: \"true\"}\n  - {guideline: conventions, text: \"# Conventions\\n\"}\n")
	p.MustRun("propose", "setup.yaml")
	p.Write(".jigflow/guidelines/conventions.md", "# Ours\n")
	before := tree(t, p)

	term := p.StartInTerminal("approve", "P-1")
	term.Expect("[y/N] ")
	term.Type("y\n")
	r := term.Wait()
	if r.ExitCode != 1 || !strings.Contains(r.Output, `Guideline "conventions" already exists`) {
		t.Fatalf("approving a Guideline that exists exited %d:\n%s", r.ExitCode, r.Output)
	}
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("a Proposal not applied changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}

	p.Write("bad.yaml", "summary: no command\nitems:\n  - {gate: lint}\n")
	if r := p.RunInSession("A", "propose", "bad.yaml"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "needs the Gate's name and a cmd") {
		t.Errorf("proposing a Gate with no command exited %d: %s", r.ExitCode, r.Stderr)
	}
}

func TestInitLeavesTheProjectWithJflsMCPServerRegistered(t *testing.T) {
	p := initialised(t)
	p.MustRun("init", "--adapter", "claude-code")

	if jfl := mcpServers(t, p.Read(".mcp.json"))["jfl"]; jfl["command"] != "jfl" || !slices.Equal(anyStrings(jfl["args"]), []string{"mcp"}) {
		t.Errorf(".mcp.json jfl server = %v, want jfl mcp", jfl)
	}
}

func TestInitThroughAgentsMdPrintsTheCommandThatRegistersJflsMCPServer(t *testing.T) {
	p := initialised(t)
	if r := p.MustRun("init", "--adapter", "agents-md"); !strings.Contains(r.Stdout, "codex mcp add jfl -- jfl mcp") {
		t.Errorf("init through agents-md = %q, want the command that registers jfl mcp", r.Stdout)
	}
}
