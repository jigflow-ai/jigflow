package main_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

func TestInitSetsAProjectUpWithTheLarapilotStylePlaybook(t *testing.T) {
	p := bin.NewProject(t)
	p.Write("go.mod", "module example.com/shop\n\ngo 1.26\n")

	r := p.MustRun("init", "--playbook", "larapilot", "--adapter", "claude-code")
	for _, want := range []string{
		"extending the Larapilot-style Playbook",
		`give Gate "tests" the command go test ./...`,
		`give Gate "lint" the command go vet ./...`,
		`add Guideline "conventions"`,
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("init output should contain %q:\n%s", want, r.Stdout)
		}
	}
	p.MustRun("check")
	approveInTerminal(t, p, "P-1")
	if r := p.MustRun("simulate", "Task"); !strings.Contains(r.Stdout, "tests (go test ./...), lint (go vet ./...)") {
		t.Errorf("the Task's Gates should run the project's commands once approved:\n%s", r.Stdout)
	}
}

// larapilot is a project whose Playbook extends the Larapilot-style
// Playbook built into jfl, changing nothing.
func larapilot(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: shop\nextends: {builtin: larapilot}\n")
	return p
}

// The CI run of every shipped Playbook: it passes check, and simulate walks
// each of its Artifact Types from inception or adopt to a Task's commit.
func TestTheLarapilotStylePlaybookPassesCheckAndSimulatesEveryType(t *testing.T) {
	p := larapilot(t)

	if r := p.MustRun("check"); firstLine(r.Stdout) != `Playbook "shop": no problems` {
		t.Errorf("check = %q", r.Stdout)
	}
	for typ, want := range map[string][]string{
		"PRD": {
			"1. inception → in-review → approved → specified",
			"2. adopt → in-review → approved → specified",
		},
		"Requirement": {
			"1. specified → planned → done",
			"2. specified → dropped",
		},
		"Story": {"1. planned → accepted"},
		"Task":  {"1. ready → in-progress → in-review → reviewed → done"},
	} {
		r := p.MustRun("simulate", typ)
		if got := pathLines(r.Stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("simulate %s paths =\n%s\nwant\n%s", typ, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	task := p.MustRun("simulate", "Task").Stdout
	for _, want := range []string{
		"ready: Skill /implement; Readiness: every \"blocked_by\" item is done",
		"→ in-review; Gates: tests (no command yet), lint (no command yet)",
		"in-review: Skill /review",
		"reviewed: no Binding, human work",
		"→ done; Human Transition; Actions: commit",
	} {
		if !strings.Contains(task, want) {
			t.Errorf("simulate Task should show %q:\n%s", want, task)
		}
	}
}

// approvedPRD is a Larapilot-style project whose PRD-1, written by an
// agent for an existing codebase, a person has approved.
func approvedPRD(t *testing.T) *clitest.Project {
	t.Helper()
	p := larapilot(t)
	p.MustRun("create", "PRD", "--title", "Shop checkout", "--status", "adopt")
	if first, _ := agentNext(t, p); first != `run /adopt on PRD-1 "Shop checkout"` {
		t.Fatalf("next = %q, want /adopt on PRD-1", first)
	}
	agentMove(t, p, "PRD-1", "in-review", 0)
	humanMove(t, p, "PRD-1", "approved")
	return p
}

func TestSpecProposesRequirementsPrioritisedWithMoSCoW(t *testing.T) {
	p := approvedPRD(t)
	if first, _ := agentNext(t, p); first != `run /spec on PRD-1 "Shop checkout"` {
		t.Fatalf("next = %q, want /spec on PRD-1", first)
	}

	p.Write("wishful.yaml", "summary: specify PRD-1\nitems:\n  - {create: Requirement, title: Pay by card, fields: {priority: nice}, links: {part_of: [PRD-1]}}\n")
	if r := p.RunInSession("A", "propose", "wishful.yaml"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "must, should, could, wont") {
		t.Errorf("a priority that isn't MoSCoW: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
	p.Write("spec.yaml", `summary: specify PRD-1 as 2 Requirements
items:
  - {create: Requirement, title: Pay by card, fields: {priority: must}, links: {part_of: [PRD-1]}}
  - {create: Requirement, title: Pay by gift card, fields: {priority: could}, links: {part_of: [PRD-1]}}
  - {move: PRD-1, to: specified}
`)
	if r := p.RunInSession("A", "propose", "spec.yaml"); r.ExitCode != 0 || !strings.Contains(r.Stdout, `create Requirement "Pay by card" (priority must), part_of PRD-1`) {
		t.Fatalf("propose: exit %d, stdout %q, stderr %q", r.ExitCode, r.Stdout, r.Stderr)
	}
	approveInTerminal(t, p, "P-1")
	for id, want := range map[string]string{"REQ-1": "must", "REQ-2": "could"} {
		if got := p.Read(".jigflow/state/" + id + ".md"); !strings.Contains(got, "priority: "+want) {
			t.Errorf("%s should have priority %s:\n%s", id, want, got)
		}
	}
	if first, _ := agentNext(t, p); first != `run /plan on REQ-1 "Pay by card"` {
		t.Errorf("next = %q, want /plan on REQ-1", first)
	}
}

// gitIdentity is who commits in a test repository, whoever runs the tests.
var gitIdentity = []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}

// git runs git in the project and returns its output, trimmed.
func git(t *testing.T, p *clitest.Project, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = p.Dir
	cmd.Env = append(cmd.Environ(), gitIdentity...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAPersonAcceptingAReviewedTaskCommitsItAsOneCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	p := larapilot(t)
	p.Write(".jigflow/playbook.yaml", "name: shop\nextends: {builtin: larapilot}\ngates:\n  tests: \"true\"\n  lint: \"true\"\n")
	git(t, p, "init", "-q", "-b", "main")
	git(t, p, "add", "--all")
	git(t, p, "commit", "-q", "-m", "the project")
	for _, kv := range gitIdentity {
		k, v, _ := strings.Cut(kv, "=")
		p.Setenv(k, v)
	}
	p.MustRun("create", "Task", "--title", "Reset-token table")
	agentMove(t, p, "T-1", "in-progress", 0)
	p.Write("reset.sql", "create table reset_tokens (token text);\n")
	agentMove(t, p, "T-1", "in-review", 0)
	agentMove(t, p, "T-1", "reviewed", 0)
	if r := agentMove(t, p, "T-1", "done", 1); !strings.Contains(r.Stderr, "Human Transition") {
		t.Errorf("an agent accepting a review should be refused as a Human Transition: %s", r.Stderr)
	}
	if got := git(t, p, "log", "--format=%s"); got != "the project" {
		t.Fatalf("nothing should be committed before a person accepts the review; log:\n%s", got)
	}

	humanMove(t, p, "T-1", "done")
	if got := git(t, p, "log", "--format=%s"); got != "T-1: Reset-token table\nthe project" {
		t.Errorf("accepting T-1 should add one commit named after it; log:\n%s", got)
	}
	if got := git(t, p, "show", "--name-only", "--format=", "HEAD"); !strings.Contains(got, "reset.sql") || !strings.Contains(got, ".jigflow/state/T-1.md") {
		t.Errorf("the commit should hold the Task's change and its state:\n%s", got)
	}
	if got := git(t, p, "status", "--porcelain"); got != "" {
		t.Errorf("nothing should be left uncommitted:\n%s", got)
	}
}

func TestTheLarapilotStylePlaybookShipsSixGenericPersonasAndNothingLaravelSpecific(t *testing.T) {
	p := larapilot(t)

	p.MustRun("publish", "claude-code")
	var personas []string
	for rel := range tree(t, p) {
		if dir, name, ok := strings.Cut(rel, ".claude/skills/jigflow/personas/"); ok && dir == "" {
			personas = append(personas, strings.TrimSuffix(name, ".md"))
		}
	}
	slices.Sort(personas)
	if want := "architect engineer product-owner reviewer security-reviewer tester"; strings.Join(personas, " ") != want {
		t.Errorf("published Personas = %v, want %s", personas, want)
	}
	for rel, content := range tree(t, p) {
		for _, word := range []string{"Laravel", "PHP", "Artisan", "Composer", "Eloquent", "Blade", "Livewire", "Pest"} {
			if strings.Contains(content, word) {
				t.Errorf("%s mentions %s", rel, word)
			}
		}
	}
}

// Each Skill that ends in a Proposal or a Human Transition asks the person
// for their Confirmation where the work ends: through jfl's MCP tools, or
// by telling them what waits and where.
func TestTheLarapilotStyleSkillsEndingInAConfirmationAskThroughJflsTools(t *testing.T) {
	p := larapilot(t)
	p.MustRun("publish", "claude-code")

	for skill, tool := range map[string]string{
		"inception": "move", // in-review → approved, the person's
		"adopt":     "move",
		"spec":      "propose",
		"plan":      "propose",
		"review":    "move", // reviewed → done, accepting the review
	} {
		assertAsksThroughJflsTools(t, "the "+skill+" Skill", p.Read(".claude/skills/"+skill+"/SKILL.md"), tool)
	}
}
