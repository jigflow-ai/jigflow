package main_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
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

// trackerRepository is a Go project not set up yet, whose jfl init's Pocock
// offer points at a Base Playbook in a git repository of the test's own,
// tagged v1, keeping Tickets in a tracker through the fake Connector, with
// its tracker file left for the project to name and none of the Ticket's
// Statuses mapped.
func trackerRepository(t *testing.T) *clitest.Project {
	t.Helper()
	u := gitUpstream(t, bin.NewProject(t))
	u.p.Write(u.dir+"/playbook.yaml", `name: base
connectors:
  tracker:
    command: `+fakeConnector+`
    settings: {tracker: "", log: calls.jsonl}
`)
	u.p.Write(u.dir+"/types/ticket.yaml", strings.Replace(baseTicket, "prefix: T\n", "prefix: T\nstore: tracker\n", 1))
	u.commit("Tickets in a tracker")
	u.git("tag", "-f", "v1")
	p := bin.NewProject(t)
	p.Setenv("JFL_POCOCK_GIT", u.URL)
	p.Setenv("JFL_POCOCK_REF", "v1")
	p.Write("go.mod", "module example.com/shop\n\ngo 1.26\n")
	return p
}

// everyAnswer is every answer jfl init needs for trackerRepository, as flags.
var everyAnswer = []string{"init", "--playbook", "pocock", "--adapter", "claude-code",
	"--setting", "tracker.tracker=tracker.json",
	"--label", "Ticket.ready-for-agent=ready-for-agent", "--label", "Ticket.in-progress=status: doing", "--label", "Ticket.done=done"}

func TestAnAgentSessionSetsAProjectUpWithEveryAnswerAsAFlag(t *testing.T) {
	p := trackerRepository(t)

	r := p.RunInSession("A", everyAnswer...)
	if r.ExitCode != 0 {
		t.Fatalf("an agent session's init with every answer exited %d: %s", r.ExitCode, r.Stderr)
	}
	for _, want := range []string{"extending the Pocock Playbook", `Connector "tracker": set up`,
		"proposed P-1: set up the Gates and a starter Guideline for the project's toolchain (3 changes, waiting for a human)",
		"P-1 waits for a person to approve or reject it with jfl approve P-1 or jfl reject P-1 in a terminal, or in the Dashboard (jfl ui)"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("init output should contain %q:\n%s", want, r.Stdout)
		}
	}
	p.MustRun("check")
	setTracker(t, p, map[string]any{"next": 41})
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-41", "in-progress")
	if i := item(t, p, "41"); fmt.Sprint(i.Labels) != "[status: doing]" {
		t.Errorf("T-41 in progress is labelled %v, want the label the agent gave", i.Labels)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".claude/skills/implement/SKILL.md")); err != nil {
		t.Errorf("init didn't publish through the Adapter the agent gave: %v", err)
	}
}

// everyAnswerBut is everyAnswer without the flag giving value.
func everyAnswerBut(t *testing.T, value string) []string {
	t.Helper()
	i := slices.Index(everyAnswer, value)
	if i < 1 {
		t.Fatalf("no flag gives %q in %v", value, everyAnswer)
	}
	return slices.Delete(slices.Clone(everyAnswer), i-1, i+1)
}

func TestInitInAnAgentSessionNamesTheFlagOfAMissingAnswerAndWritesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"the Playbook": {everyAnswerBut(t, "pocock"), "--playbook larapilot|pocock|own"},
		"the Adapter":  {everyAnswerBut(t, "claude-code"), "--adapter claude-code|agents-md"},
		"a setting":    {everyAnswerBut(t, "tracker.tracker=tracker.json"), "--setting tracker.tracker=<value>"},
		"a label":      {everyAnswerBut(t, "Ticket.in-progress=status: doing"), "--label Ticket.in-progress=<label>"},
	} {
		t.Run(name, func(t *testing.T) {
			p := trackerRepository(t)
			before := tree(t, p)

			r := p.RunInSession("A", tc.args...)
			if r.ExitCode != 2 || !strings.Contains(r.Stderr, tc.want) {
				t.Errorf("an agent session's init without %s exited %d, want 2 naming %q: %s", name, r.ExitCode, tc.want, r.Stderr)
			}
			if after := tree(t, p); !maps.Equal(after, before) {
				t.Errorf("a refused init changed the project: %v", slices.Sorted(maps.Keys(after)))
			}
		})
	}
}

func TestInitRunAgainInAnAgentSessionFillsInOnlyWhatIsMissing(t *testing.T) {
	p := trackerRepository(t)
	p.MustRun("init", "--playbook", "pocock", "--adapter", "claude-code", "--label", "Ticket.in-progress=status: doing")
	if !strings.Contains(p.Read(".jigflow/playbook.yaml"), `tracker: ""`) {
		t.Fatalf("init without a terminal should leave the tracker setting empty:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
	before := tree(t, p)

	r := p.RunInSession("A", "init")
	if r.ExitCode != 2 || !strings.Contains(r.Stderr, "--setting tracker.tracker=<value>") || strings.Contains(r.Stderr, "--label") || strings.Contains(r.Stderr, "--adapter") {
		t.Errorf("an agent session's init again exited %d, want 2 naming only the setting still missing: %s", r.ExitCode, r.Stderr)
	}
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused init again changed the project")
	}

	r = p.RunInSession("A", "init", "--playbook", "own", "--setting", "tracker.tracker=tracker.json")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "keeping it, not choosing own") {
		t.Fatalf("an agent session's init again with the missing setting exited %d: %s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	pbFile := p.Read(".jigflow/playbook.yaml")
	for _, want := range []string{"extends: {git: ", "tracker: tracker.json", "in-progress: 'status: doing'"} {
		if !strings.Contains(pbFile, want) {
			t.Errorf("the Playbook file should keep the Playbook chosen and the labels given, and add the setting, with %q:\n%s", want, pbFile)
		}
	}
}

func TestTheProposalInitPutsForwardInAnAgentSessionIsThatSessions(t *testing.T) {
	for name, confirm := range map[string]func(t *testing.T, p *clitest.Project){
		"at a terminal": func(t *testing.T, p *clitest.Project) {
			if r := approveInTerminal(t, p, "P-1"); !strings.Contains(r.Output, "P-1 from agent session A: set up the Gates") {
				t.Errorf("the terminal should ask about P-1 as agent session A's:\n%s", r.Output)
			}
		},
		"through the approve tool": func(t *testing.T, p *clitest.Project) {
			m := p.StartMCPWith("A", eliciting)
			m.Answer(clitest.Accept("approve"))
			if r := m.MustCallTool("approve", map[string]any{"proposal": "P-1"}); r.IsError || firstLine(r.Text) != "approved P-1 as one unit:" {
				t.Errorf("approve = %+v, want P-1 approved", r)
			}
			if forms := m.Forms(); len(forms) != 1 || !strings.HasPrefix(forms[0].Message, "P-1 from agent session A: set up the Gates") {
				t.Errorf("the form should ask about P-1 as agent session A's: %+v", forms)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := trackerRepository(t)
			if r := p.RunInSession("A", everyAnswer...); r.ExitCode != 0 {
				t.Fatalf("an agent session's init exited %d: %s", r.ExitCode, r.Stderr)
			}

			if r := p.RunInSession("A", "approve", "P-1"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "Only a human can approve P-1.") {
				t.Errorf("the agent session approving its init's Proposal exited %d, want 1: %s", r.ExitCode, r.Stderr)
			}

			confirm(t, p)
			if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: approved") {
				t.Errorf("P-1 should be recorded as approved:\n%s", got)
			}
			if !strings.Contains(p.Read(".jigflow/guidelines/conventions.md"), "go test ./...") {
				t.Errorf("approving P-1 should add the starter Guideline")
			}
		})
	}
}

func TestInitInAnAgentSessionNeverAsksEvenAtATerminal(t *testing.T) {
	p := trackerRepository(t)

	term := p.StartInTerminalInSession("A", everyAnswerBut(t, "tracker.tracker=tracker.json")...)
	r := term.Wait()
	if r.ExitCode != 2 || !strings.Contains(r.Output, "--setting tracker.tracker=<value>") || strings.Contains(r.Output, `Connector "tracker": tracker: `) {
		t.Errorf("an agent session's init at a terminal exited %d, want 2 naming the flag without asking:\n%s", r.ExitCode, r.Output)
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

// describedTrackerBase is trackerBase with the fake Connector describing
// its settings, which the Base Playbook declares none of but the log: a
// tracker file, and a project and a yes/no closes per Artifact Type, are
// required.
func describedTrackerBase(t *testing.T) *clitest.Project {
	t.Helper()
	p := trackerBase(t)
	p.Write("base/playbook.yaml", strings.Replace(p.Read("base/playbook.yaml"), `    settings: {tracker: "", log: calls.jsonl}`, "    args: [--describe=describe.json]\n    settings: {log: calls.jsonl}", 1))
	p.Write("describe.json", `{"settings": [
  {"name": "tracker", "kind": "text", "required": true, "help": "the file that keeps the fake tracker"},
  {"name": "log", "kind": "text", "help": "the file every request is logged to"},
  {"name": "verbose", "kind": "yesno", "help": "say more"},
  {"name": "project", "kind": "text", "required": true, "per_type": true, "help": "the project that keeps this Type's items"},
  {"name": "closes", "kind": "yesno", "required": true, "per_type": true, "help": "close an item when it is done"}
]}`)
	return p
}

func TestInitAsksForTheRequiredSettingsAConnectorDescribesInATerminal(t *testing.T) {
	p := describedTrackerBase(t)

	term := p.StartInTerminal("init", "--adapter", "agents-md", "--label", "Ticket.ready-for-agent=ready-for-agent", "--label", "Ticket.in-progress=in-progress", "--label", "Ticket.done=done")
	term.Expect(`Connector "tracker": tracker (the file that keeps the fake tracker): `)
	term.Type("tracker.json\n")
	term.Expect(`Connector "tracker", Ticket: closes (close an item when it is done, yes or no): `)
	term.Type("yes\n")
	term.Expect(`Connector "tracker", Ticket: project (the project that keeps this Type's items): `)
	term.Type("shop\n")
	r := term.Wait()
	if r.ExitCode != 0 {
		t.Fatalf("init exited %d; terminal:\n%s", r.ExitCode, r.Output)
	}
	if strings.Contains(r.Output, "verbose") {
		t.Errorf("init asked for verbose, which isn't required:\n%s", r.Output)
	}
	pbFile := p.Read(".jigflow/playbook.yaml")
	for _, want := range []string{"tracker: tracker.json", "closes: true", "project: shop"} {
		if !strings.Contains(pbFile, want) {
			t.Errorf("the Playbook file should say %q:\n%s", want, pbFile)
		}
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	if c := calls(t, p, "create"); len(c) != 1 || c[0].Settings["project"] != "shop" || c[0].Settings["closes"] != true {
		t.Errorf("the Connector should be sent the settings init asked for: %+v", c)
	}
}

func TestAnAgentSessionGivesTheRequiredSettingsAConnectorDescribesAsFlags(t *testing.T) {
	p := describedTrackerBase(t)
	labels := []string{"--label", "Ticket.ready-for-agent=ready-for-agent", "--label", "Ticket.in-progress=in-progress", "--label", "Ticket.done=done"}
	before := tree(t, p)

	r := p.RunInSession("A", append([]string{"init", "--adapter", "agents-md"}, labels...)...)
	if r.ExitCode != 2 || !strings.Contains(r.Stderr, "--setting tracker.tracker=<value> --setting tracker.Ticket.closes=<value> --setting tracker.Ticket.project=<value>") || strings.Contains(r.Stderr, "verbose") {
		t.Errorf("an agent session's init without the required settings exited %d, want 2 naming each: %s", r.ExitCode, r.Stderr)
	}
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused init changed the project")
	}

	r = p.RunInSession("A", append([]string{"init", "--adapter", "agents-md", "--setting", "tracker.tracker=tracker.json", "--setting", "tracker.Ticket.closes=no", "--setting", "tracker.Ticket.project=shop"}, labels...)...)
	if r.ExitCode != 0 {
		t.Fatalf("an agent session's init with the required settings exited %d: %s", r.ExitCode, r.Stderr)
	}
	pbFile := p.Read(".jigflow/playbook.yaml")
	for _, want := range []string{"tracker: tracker.json", "closes: false", "project: shop"} {
		if !strings.Contains(pbFile, want) {
			t.Errorf("the Playbook file should say %q:\n%s", want, pbFile)
		}
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
