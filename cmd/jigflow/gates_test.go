package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// gatedPlaybook is a one-Type Playbook whose in-progress → in-review
// Transition carries the given gates and actions YAML (each a list, indented
// for a field of the Transition), with a T-1 already in in-progress.
func gatedPlaybook(t *testing.T, gates, actions string) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: gated\n")
	tr := "  - from: in-progress\n    to: in-review\n"
	if gates != "" {
		tr += "    gates:\n" + gates
	}
	if actions != "" {
		tr += "    actions:\n" + actions
	}
	p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
statuses: [in-progress, in-review, done]
initial: [in-progress]
final: [done]
transitions:
`+tr+`  - from: in-review
    to: done
`)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	return p
}

// record is a script that appends its first argument to ran.log, so a test
// can see which commands ran and in what order.
const record = "#!/bin/sh\necho \"$1\" >> ran.log\n"

func TestAFailingGateRefusesTheTransition(t *testing.T) {
	p := gatedPlaybook(t, `      - name: tests
        cmd: echo "3 tests failed"; false
`, "")
	before := p.Read(".jigflow/state/T-1.md")

	r := p.Run("move", "T-1", "in-review")
	if r.ExitCode != 1 {
		t.Fatalf("move with a failing Gate exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, `Gate "tests" failed`) {
		t.Errorf("refusal %q should name the failing Gate", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "3 tests failed") {
		t.Errorf("refusal %q should include the Gate's output", r.Stderr)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestGatesRunInDeclarationOrderAndPassingGatesLetTheTransitionHappen(t *testing.T) {
	p := gatedPlaybook(t, `      - name: first
        cmd: sh record.sh first
      - name: second
        cmd: sh record.sh second && true
`, "")
	p.Write("record.sh", record)

	p.MustRun("move", "T-1", "in-review")
	if got := p.Read("ran.log"); got != "first\nsecond\n" {
		t.Errorf("Gates ran as %q, want first then second", got)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "in-review" {
		t.Errorf("status = %q, want in-review", got)
	}
}

func TestGatesAfterAFailingGateDoNotRun(t *testing.T) {
	p := gatedPlaybook(t, `      - name: first
        cmd: sh record.sh first
      - name: lint
        cmd: "false"
      - name: third
        cmd: sh record.sh third
`, "")
	p.Write("record.sh", record)

	r := p.Run("move", "T-1", "in-review")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, `Gate "lint" failed`) {
		t.Fatalf("move: exit %d, stderr %q; want refusal naming Gate lint", r.ExitCode, r.Stderr)
	}
	if got := p.Read("ran.log"); got != "first\n" {
		t.Errorf("Gates ran as %q, want only first (declaration order, stopping at the failure)", got)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "in-progress" {
		t.Errorf("status = %q, want in-progress", got)
	}
}

func TestActionsRunAfterTheTransitionSucceedsAndTheirOutcomeIsReported(t *testing.T) {
	// The Action records the Status it finds on disk, proving it runs after
	// the Transition has been saved.
	p := gatedPlaybook(t, `      - name: tests
        cmd: "true"
`, `      - name: commit
        cmd: grep '^status:' .jigflow/state/T-1.md >> ran.log && echo "committed T-1"
`)

	r := p.MustRun("move", "T-1", "in-review")
	if got := p.Read("ran.log"); got != "status: in-review\n" {
		t.Errorf("the Action saw %q, want it to run after the move to in-review", got)
	}
	if !strings.Contains(r.Stdout, `Action "commit" succeeded`) || !strings.Contains(r.Stdout, "committed T-1") {
		t.Errorf("move output %q should report the Action's outcome and output", r.Stdout)
	}
}

func TestActionsDoNotRunWhenAGateRefusesTheTransition(t *testing.T) {
	p := gatedPlaybook(t, `      - name: tests
        cmd: "false"
`, `      - name: commit
        cmd: sh record.sh commit
`)
	p.Write("record.sh", record)

	if r := p.Run("move", "T-1", "in-review"); r.ExitCode != 1 {
		t.Fatalf("move with a failing Gate exited %d, want 1", r.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "ran.log")); !os.IsNotExist(err) {
		t.Errorf("an Action ran although the Transition was refused: %s", p.Read("ran.log"))
	}
}

func TestAFailingActionIsReportedButTheTransitionStands(t *testing.T) {
	p := gatedPlaybook(t, "", `      - name: notify
        cmd: echo "no network"; exit 3
      - name: commit
        cmd: sh record.sh commit
`)
	p.Write("record.sh", record)

	r := p.Run("move", "T-1", "in-review")
	if r.ExitCode != 1 {
		t.Errorf("move with a failing Action exited %d, want 1", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "T-1: in-progress → in-review") {
		t.Errorf("stdout %q should still report the Transition", r.Stdout)
	}
	if !strings.Contains(r.Stderr, `Action "notify" failed`) || !strings.Contains(r.Stderr, "no network") {
		t.Errorf("stderr %q should name the failing Action and include its output", r.Stderr)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "in-review" {
		t.Errorf("status = %q, want in-review: a failing Action does not undo the Transition", got)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "ran.log")); !os.IsNotExist(err) {
		t.Errorf("an Action after the failing one ran: %s", p.Read("ran.log"))
	}
}

// A Gate may leave its command to the project's Playbook file, which gives
// it one by name; an Action can't.
func TestAGateNeedsANameAndAnActionANameAndACommand(t *testing.T) {
	for name, tc := range map[string]struct{ gates, actions, want string }{
		"gate without a name":    {gates: "      - cmd: \"true\"\n", want: `a Gate on "in-progress" → "in-review" needs a name`},
		"action without a cmd":   {actions: "      - name: commit\n", want: `an Action on "in-progress" → "in-review" needs a name and a cmd`},
		"action without a name":  {actions: "      - cmd: \"true\"\n", want: `an Action on "in-progress" → "in-review" needs a name and a cmd`},
		"misspelt command field": {gates: "      - name: tests\n        command: \"true\"\n", want: "command"},
	} {
		t.Run(name, func(t *testing.T) {
			p := bin.NewProject(t)
			p.Write(".jigflow/playbook.yaml", "name: gated\n")
			tr := "  - from: in-progress\n    to: in-review\n"
			if tc.gates != "" {
				tr += "    gates:\n" + tc.gates
			}
			if tc.actions != "" {
				tr += "    actions:\n" + tc.actions
			}
			p.Write(".jigflow/types/ticket.yaml", "name: Ticket\nprefix: T\nstatuses: [in-progress, in-review]\ninitial: [in-progress]\ntransitions:\n"+tr)

			r := p.Run("next")
			if r.ExitCode != 1 || !strings.Contains(r.Stderr, "ticket.yaml") || !strings.Contains(r.Stderr, tc.want) {
				t.Errorf("exit %d, stderr %q; want a refusal naming ticket.yaml and %q", r.ExitCode, r.Stderr, tc.want)
			}
		})
	}
}

func TestGatesAndActionsKnowWhichArtifactAndTransitionTheyRunFor(t *testing.T) {
	p := gatedPlaybook(t, `      - name: tests
        cmd: echo "gate $JFL_ARTIFACT $JFL_FROM $JFL_TO" >> ran.log
`, `      - name: commit
        cmd: echo "action $JFL_ARTIFACT $JFL_FROM $JFL_TO" >> ran.log
`)

	p.MustRun("move", "T-1", "in-review")
	want := "gate T-1 in-progress in-review\naction T-1 in-progress in-review\n"
	if got := p.Read("ran.log"); got != want {
		t.Errorf("commands saw %q, want %q", got, want)
	}
}

func TestTheProjectsPlaybookFileGivesAGateItsCommandByName(t *testing.T) {
	p := gatedPlaybook(t, `      - name: tests
      - name: lint
        cmd: sh record.sh playbook-lint
`, "")
	p.Write("record.sh", record)
	p.Write(".jigflow/playbook.yaml", "name: gated\ngates:\n  tests: sh record.sh project-tests\n  lint: sh record.sh project-lint\n")

	p.MustRun("move", "T-1", "in-review")
	if got := p.Read("ran.log"); got != "project-tests\nproject-lint\n" {
		t.Errorf("Gates ran as %q, want the Playbook file's commands for tests and lint", got)
	}
}

func TestAGateWithNoCommandRefusesTheTransitionSayingWhereToGiveIt(t *testing.T) {
	p := gatedPlaybook(t, "      - name: tests\n", "")
	before := p.Read(".jigflow/state/T-1.md")

	r := p.Run("move", "T-1", "in-review")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, `Gate "tests" has no command: give it one under gates in .jigflow/playbook.yaml`) {
		t.Fatalf("move with a Gate with no command: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\n%s", after)
	}
	if r := p.MustRun("simulate", "Ticket"); !strings.Contains(r.Stdout, "tests (no command yet)") {
		t.Errorf("simulate should show the Gate has no command yet:\n%s", r.Stdout)
	}
}
