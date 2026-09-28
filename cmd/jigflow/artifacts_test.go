package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"go.yaml.in/yaml/v3"
)

// ticketPlaybook is a minimal Playbook: one Artifact Type in its own YAML file,
// with a small Playbook file alongside it.
func ticketPlaybook(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: skeleton\n")
	p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
statuses: [ready-for-agent, in-progress, in-review, done]
initial: [ready-for-agent]
final: [done]
bindings:
  ready-for-agent: implement
  in-progress: implement
transitions:
  - from: ready-for-agent
    to: in-progress
  - from: in-progress
    to: in-review
  - from: in-review
    to: in-progress
  - from: in-review
    to: done
`)
	writeSkill(p, "implement", true)
	return p
}

// frontmatter parses the YAML frontmatter of an Artifact file.
func frontmatter(t *testing.T, content string) map[string]string {
	t.Helper()
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		t.Fatalf("Artifact file does not start with a frontmatter block:\n%s", content)
	}
	fm, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("Artifact file has no closing frontmatter delimiter:\n%s", content)
	}
	var m map[string]string
	if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
		t.Fatalf("frontmatter is not YAML: %v\n%s", err, fm)
	}
	return m
}

func TestCreateWritesMarkdownArtifactWithFrontmatter(t *testing.T) {
	p := ticketPlaybook(t)

	r := p.MustRun("create", "Ticket", "--title", "Add login page")
	if !strings.Contains(r.Stdout, "T-1") {
		t.Errorf("create output %q does not name the new Artifact T-1", r.Stdout)
	}

	got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))
	want := map[string]string{"id": "T-1", "type": "Ticket", "status": "ready-for-agent", "title": "Add login page"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("frontmatter %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestCreateNumbersArtifactsWithTheTypesPrefix(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "First")
	p.MustRun("create", "Ticket", "--title", "Second")

	if got := frontmatter(t, p.Read(".jigflow/state/T-2.md"))["title"]; got != "Second" {
		t.Errorf("T-2 title = %q, want %q", got, "Second")
	}
}

func TestCreateRefusesAStartingStatusThatIsNotInitial(t *testing.T) {
	p := ticketPlaybook(t)

	r := p.Run("create", "Ticket", "--title", "Skip ahead", "--status", "in-review")
	if r.ExitCode == 0 {
		t.Fatalf("create into a non-initial Status exited 0; stdout: %s", r.Stdout)
	}
	if !strings.Contains(r.Stderr, `can't start in "in-review"`) || !strings.Contains(r.Stderr, "ready-for-agent") {
		t.Errorf("refusal %q should name the Status and the allowed starting Statuses", r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".jigflow/state/T-1.md")); !os.IsNotExist(err) {
		t.Errorf("a refused create must not write an Artifact file (stat err: %v)", err)
	}
}

func TestCreateAcceptsAnExplicitInitialStatus(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Explicit", "--status", "ready-for-agent")

	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "ready-for-agent" {
		t.Errorf("status = %q, want ready-for-agent", got)
	}
}

func TestMoveThroughADeclaredTransitionUpdatesTheStatus(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add login page")

	r := p.MustRun("move", "T-1", "in-progress")
	if !strings.Contains(r.Stdout, "ready-for-agent") || !strings.Contains(r.Stdout, "in-progress") {
		t.Errorf("move output %q should report the old and new Status", r.Stdout)
	}
	fm := frontmatter(t, p.Read(".jigflow/state/T-1.md"))
	if fm["status"] != "in-progress" {
		t.Errorf("status = %q, want in-progress", fm["status"])
	}
	if fm["title"] != "Add login page" || fm["id"] != "T-1" {
		t.Errorf("move changed other frontmatter: %v", fm)
	}
}

func TestMoveRefusesAnUndeclaredTransition(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	before := p.Read(".jigflow/state/T-1.md")

	r := p.Run("move", "T-1", "done")
	if r.ExitCode == 0 {
		t.Fatalf("an undeclared Transition exited 0; stdout: %s", r.Stdout)
	}
	if !strings.Contains(r.Stderr, `"ready-for-agent" → "done" is not a declared Transition`) {
		t.Errorf("refusal %q should name the undeclared Transition", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "in-progress") {
		t.Errorf("refusal %q should list the Transitions that are declared from here", r.Stderr)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestMoveKeepsTheArtifactBody(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	p.Write(".jigflow/state/T-1.md", p.Read(".jigflow/state/T-1.md")+"Users sign in with email.\n")

	p.MustRun("move", "T-1", "in-progress")
	if got := p.Read(".jigflow/state/T-1.md"); !strings.HasSuffix(got, "Users sign in with email.\n") {
		t.Errorf("move lost the body:\n%s", got)
	}
}

func TestMoveRefusesAnUnknownArtifact(t *testing.T) {
	p := ticketPlaybook(t)
	r := p.Run("move", "T-9", "in-progress")
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, "T-9") {
		t.Errorf("move of a missing Artifact: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
}
