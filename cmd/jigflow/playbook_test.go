package main_test

import (
	"strings"
	"testing"
)

func TestCommandsRefuseAProjectWithoutAPlaybook(t *testing.T) {
	p := bin.NewProject(t)
	r := p.Run("next")
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, "no Playbook found") {
		t.Errorf("next without a Playbook: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

func TestAMisspeltArtifactTypeFieldIsRefused(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "bindings:", "bindngs:", 1))

	r := p.Run("next")
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, "ticket.yaml") || !strings.Contains(r.Stderr, "bindngs") {
		t.Errorf("a misspelt field should be refused naming the file and field: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

func TestCreateRefusesAnUndeclaredArtifactType(t *testing.T) {
	p := ticketPlaybook(t)
	r := p.Run("create", "Epic", "--title", "Big")
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, `unknown Artifact Type "Epic"`) || !strings.Contains(r.Stderr, "Ticket") {
		t.Errorf("create of an undeclared Type: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

func TestMoveRejectsAnIdThatIsAPath(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write("outside.md", "---\nid: X-1\ntype: Ticket\nstatus: ready-for-agent\ntitle: x\n---\n")
	r := p.Run("move", "../../outside", "in-progress")
	if r.ExitCode == 0 {
		t.Errorf("move with a path for an id exited 0; stdout %q", r.Stdout)
	}
}
