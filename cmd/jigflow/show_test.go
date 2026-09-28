package main_test

import (
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
)

func TestShowPrintsAnArtifactWithItsBody(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	p.Write(".jigflow/state/T-1.md", strings.Replace(p.Read(".jigflow/state/T-1.md"), "---\n\n", "---\n\nA form with an email and a password.\n", 1))
	p.MustRun("comment", "T-1", "Use the existing session store.")

	r := p.RunInSession("A", "show", "T-1")
	if r.ExitCode != 0 {
		t.Fatalf("show exited %d: %s", r.ExitCode, r.Stderr)
	}
	for _, want := range []string{
		`T-1 Ticket "Add login page": ready-for-agent`,
		"A form with an email and a password.",
		"Use the existing session store.",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("show should print %q:\n%s", want, r.Stdout)
		}
	}
	if r := p.Run("show", "T-9"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "T-9") {
		t.Errorf("show of an unknown Artifact exited %d: %s", r.ExitCode, r.Stderr)
	}
}

func TestShowReadsATrackerArtifactsBodyAndComments(t *testing.T) {
	p, gh := githubPlaybook(t)
	n := gh.Add(fakegithub.Issue{Title: "Reset-token table", Body: "Tokens expire after an hour.", Labels: []string{"ticket", "ready-for-agent"}})
	gh.Edit(n, func(i *fakegithub.Issue) { i.Comments = append(i.Comments, "Store them hashed.") })

	r := p.RunInSession("A", "show", "T-41")
	if r.ExitCode != 0 {
		t.Fatalf("show exited %d: %s", r.ExitCode, r.Stderr)
	}
	for _, want := range []string{
		`T-41 Ticket "Reset-token table": ready-for-agent`,
		"Tokens expire after an hour.",
		"Store them hashed.",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("show should print %q:\n%s", want, r.Stdout)
		}
	}
}
