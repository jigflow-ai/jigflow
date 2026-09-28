package main_test

import (
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

func TestTheStoreRecordsAContentHashForEveryArtifactItWrites(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	created := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["hash"]
	if created == "" {
		t.Fatalf("create wrote no content hash:\n%s", p.Read(".jigflow/state/T-1.md"))
	}

	p.MustRun("move", "T-1", "in-progress")
	moved := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["hash"]
	if moved == "" || moved == created {
		t.Errorf("move should record a new content hash; before %q, after %q", created, moved)
	}
}

// assertMoveRefusedAsEditedOutside checks that a move of the Artifact id was
// refused because its frontmatter was changed outside jfl, and that the
// refusal left its file as it was.
func assertMoveRefusedAsEditedOutside(t *testing.T, r clitest.Result, id, why, before, after string) {
	t.Helper()
	if r.ExitCode != 1 {
		t.Fatalf("move of an Artifact whose frontmatter was edited outside jfl exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, id+":") || !strings.Contains(r.Stderr, why) {
		t.Errorf("refusal %q should name the Artifact %s and say %q", r.Stderr, id, why)
	}
	if !strings.Contains(r.Stderr, "jfl move") {
		t.Errorf("refusal %q should say Statuses change only through jfl move", r.Stderr)
	}
	if after != before {
		t.Errorf("a refused move changed the Artifact file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestAStatusChangedOutsideJflRefusesTheTransition(t *testing.T) {
	for _, session := range []string{"", "s-1"} {
		t.Run("session="+session, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.MustRun("create", "Ticket", "--title", "Add login page")
			p.MustRun("move", "T-1", "in-progress")
			// Skip ahead by hand: in-progress → in-review is declared, so
			// only the edit itself can refuse the next move.
			edited := strings.Replace(p.Read(".jigflow/state/T-1.md"), "status: in-progress", "status: in-review", 1)
			p.Write(".jigflow/state/T-1.md", edited)

			r := p.RunInSession(session, "move", "T-1", "done")
			assertMoveRefusedAsEditedOutside(t, r, "T-1", "frontmatter was changed outside jfl", edited, p.Read(".jigflow/state/T-1.md"))
		})
	}
}

func TestABodyEditMadeOutsideJflIsAcceptedAfterRevalidationWhenTheArtifactNextMoves(t *testing.T) {
	for _, session := range []string{"", "s-1"} {
		t.Run("session="+session, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.MustRun("create", "Ticket", "--title", "Add login page")
			p.Write(".jigflow/state/T-1.md", p.Read(".jigflow/state/T-1.md")+"Users sign in with email.\n")

			r := p.RunInSession(session, "move", "T-1", "in-progress")
			if r.ExitCode != 0 {
				t.Fatalf("move after a body edit exited %d; stderr: %s", r.ExitCode, r.Stderr)
			}
			if !strings.Contains(r.Stdout, "T-1: body edited outside jfl, re-validated") {
				t.Errorf("move output %q should report the re-validated body edit", r.Stdout)
			}
			got := p.Read(".jigflow/state/T-1.md")
			if !strings.HasSuffix(got, "Users sign in with email.\n") || frontmatter(t, got)["status"] != "in-progress" {
				t.Errorf("move should keep the edited body and apply the Transition:\n%s", got)
			}

			// The move recorded the edited body's hash, so the next move
			// finds nothing to re-validate.
			r = p.RunInSession(session, "move", "T-1", "in-review")
			if r.ExitCode != 0 || strings.Contains(r.Stdout, "re-validated") {
				t.Errorf("the move after should see no outside edit; exit %d, stdout %q, stderr %q", r.ExitCode, r.Stdout, r.Stderr)
			}
		})
	}
}

func TestAnyFrontmatterChangeMadeOutsideJflRefusesTheTransition(t *testing.T) {
	const handWritten = "---\nid: T-2\ntype: Ticket\nstatus: ready-for-agent\ntitle: By hand\n---\n"
	for _, tc := range []struct {
		name, id, why string
		edit          func(p *clitest.Project)
	}{
		{"title", "T-1", "frontmatter was changed outside jfl", func(p *clitest.Project) {
			p.Write(".jigflow/state/T-1.md", strings.Replace(p.Read(".jigflow/state/T-1.md"), "title: Add login page", "title: Add signup page", 1))
		}},
		{"hash removed", "T-1", "no content hash", func(p *clitest.Project) {
			fm := p.Read(".jigflow/state/T-1.md")
			i := strings.Index(fm, "hash:")
			j := i + strings.Index(fm[i:], "\n") + 1
			p.Write(".jigflow/state/T-1.md", fm[:i]+fm[j:])
		}},
		{"file written by hand", "T-2", "no content hash", func(p *clitest.Project) {
			p.Write(".jigflow/state/T-2.md", handWritten)
		}},
		{"file copied under another id", "T-2", "is not T-2", func(p *clitest.Project) {
			p.Write(".jigflow/state/T-2.md", p.Read(".jigflow/state/T-1.md"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.MustRun("create", "Ticket", "--title", "Add login page")
			tc.edit(p)
			before := p.Read(".jigflow/state/" + tc.id + ".md")

			r := p.Run("move", tc.id, "in-progress")
			assertMoveRefusedAsEditedOutside(t, r, tc.id, tc.why, before, p.Read(".jigflow/state/"+tc.id+".md"))
		})
	}
}

func TestReadsNeverFailBecauseOfBodyEdits(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(file string) string
	}{
		{"body that looks like frontmatter", func(file string) string {
			return file + "---\nstatus: done\nhash: nonsense\n---\n\n---\n"
		}},
		{"body emptied up to the closing delimiter", func(file string) string {
			return strings.TrimRight(file, "\n") // ends in "---", no newline
		}},
		{"body with Windows line endings", func(file string) string {
			return file + "Users sign in with email.\r\n\r\nNo passwords.\r\n"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := ticketPlaybook(t)
			p.MustRun("create", "Ticket", "--title", "Add login page")
			p.Write(".jigflow/state/T-1.md", tc.edit(p.Read(".jigflow/state/T-1.md")))

			if r := p.MustRun("next"); !strings.Contains(r.Stdout, "T-1") {
				t.Errorf("next output %q should still hand out T-1", r.Stdout)
			}
			r := p.MustRun("move", "T-1", "in-progress")
			if !strings.Contains(r.Stdout, "re-validated") {
				t.Errorf("move output %q should report the re-validated body edit", r.Stdout)
			}
			if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["status"]; got != "in-progress" {
				t.Errorf("status = %q, want in-progress", got)
			}
		})
	}
}

func TestABodyEditMadeByAGateIsKeptByTheTransition(t *testing.T) {
	p := gatedPlaybook(t, `      - name: notes
        cmd: echo "Reviewed by the gate." >> .jigflow/state/$JFL_ARTIFACT.md
`, "")

	p.MustRun("move", "T-1", "in-review")
	got := p.Read(".jigflow/state/T-1.md")
	if !strings.HasSuffix(got, "Reviewed by the gate.\n") || frontmatter(t, got)["status"] != "in-review" {
		t.Errorf("the move should keep the Gate's body edit and apply the Transition:\n%s", got)
	}
	if r := p.MustRun("move", "T-1", "done"); strings.Contains(r.Stdout, "re-validated") {
		t.Errorf("the Gate's body edit should be recorded by the move it ran for; next move said %q", r.Stdout)
	}
}

func TestAFrontmatterChangeMadeByAGateRefusesTheTransition(t *testing.T) {
	p := gatedPlaybook(t, `      - name: sneaky
        cmd: "sed -i 's/^title: .*/title: Renamed/' .jigflow/state/$JFL_ARTIFACT.md"
`, "")

	r := p.Run("move", "T-1", "in-review")
	after := p.Read(".jigflow/state/T-1.md")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "T-1: its frontmatter was changed outside jfl") {
		t.Fatalf("a Gate changing frontmatter should refuse the move; exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
	if fm := frontmatter(t, after); fm["status"] != "in-progress" || fm["title"] != "Renamed" {
		t.Errorf("the refused move should leave the file as the Gate left it:\n%s", after)
	}
}
