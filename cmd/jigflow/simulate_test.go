package main_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

func TestSimulatePrintsEachPathFromAnInitialStatusToAFinalOneWithoutRevisitingAStatus(t *testing.T) {
	p := ticketPlaybook(t)

	r := p.MustRun("simulate", "Ticket")
	want := `Ticket: 1 path from an initial Status to a final one

1. ready-for-agent → in-progress → in-review → done
`
	if !strings.HasPrefix(r.Stdout, want) {
		t.Errorf("simulate printed\n%s\nwant it to start with\n%s", r.Stdout, want)
	}
}

// pathLines returns the line naming each Path in simulate's output.
func pathLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if n, _, ok := strings.Cut(line, ". "); ok && n != "" && strings.Trim(n, "0123456789") == "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestSimulateFollowsEveryBranchFromEveryInitialStatus(t *testing.T) {
	p := pocockPlaybook(t)

	r := p.MustRun("simulate", "Issue")
	if want := "Issue: 4 paths from an initial Status to a final one"; firstLine(r.Stdout) != want {
		t.Errorf("simulate = %q, want %q", firstLine(r.Stdout), want)
	}
	// needs-info only leads back to needs-triage, which the Path has
	// already visited, so no Path goes through it.
	want := []string{
		"1. needs-triage → ready-for-agent → done",
		"2. needs-triage → ready-for-human → done",
		"3. needs-triage → wontfix",
		"4. ready-for-agent → done",
	}
	if got := pathLines(r.Stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("paths =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSimulateMarksSkillsReadinessGuardsGatesActionsAndHumanWork(t *testing.T) {
	p := pocockPlaybook(t)

	for typ, want := range map[string]string{
		"Ticket": `Ticket: 1 path from an initial Status to a final one

1. ready-for-agent → in-progress → in-review → ready-to-merge → done
   ready-for-agent: Skill /implement; Readiness: every "blocked_by" item is done
     → in-progress
   in-progress: Skill /implement
     → in-review; Gates: tests (test -f tests-pass)
   in-review: Skill /code-review
     → ready-to-merge; Gates: lint (true)
   ready-to-merge: no Binding, human work
     → done; Human Transition; Actions: commit (sh record.sh "merge $JFL_ARTIFACT")
   done: final
`,
		"Spec": `Spec: 1 path from an initial Status to a final one

1. ready-for-agent → ticketed
   ready-for-agent: Skill /to-tickets
     → ticketed; Guards: at least 1 item(s) link here via "part_of"
   ticketed: final
`,
	} {
		if r := p.MustRun("simulate", typ); r.Stdout != want {
			t.Errorf("simulate %s printed\n%s\nwant\n%s", typ, r.Stdout, want)
		}
	}
}

// tree maps every file under the project to its content.
func tree(t *testing.T, p *clitest.Project) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(p.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		rel, _ := filepath.Rel(p.Dir, path)
		files[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSimulateWritesNoStateAndRunsNoGatesOrActions(t *testing.T) {
	p := ticketPlaybook(t)
	ticket := strings.Replace(p.Read(".jigflow/types/ticket.yaml"), `    to: in-review
`, `    to: in-review
    gates:
      - {name: tests, cmd: touch gate-ran}
    actions:
      - {name: notify, cmd: touch action-ran}
`, 1)
	p.Write(".jigflow/types/ticket.yaml", ticket)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	before := tree(t, p)

	r := p.MustRun("simulate", "Ticket")
	if want := "→ in-review; Gates: tests (touch gate-ran); Actions: notify (touch action-ran)"; !strings.Contains(r.Stdout, want) {
		t.Errorf("simulate printed\n%s\nwant it to show %q", r.Stdout, want)
	}
	after := tree(t, p)
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("simulate wrote %s", rel)
		}
	}
	for rel, content := range before {
		if after[rel] != content {
			t.Errorf("simulate changed %s", rel)
		}
	}
	if r = p.RunInSession("A", "simulate", "Ticket"); r.ExitCode != 0 {
		t.Fatalf("an agent session's simulate exited %d: %s", r.ExitCode, r.Stderr)
	}
	if after := tree(t, p); len(after) != len(before) {
		t.Errorf("an agent session's simulate wrote files: %d before, %d after", len(before), len(after))
	}
}

func TestSimulateRefusesAnUnknownArtifactTypeOrAMissingOne(t *testing.T) {
	p := pocockPlaybook(t)

	r := p.Run("simulate", "Tiket")
	want := `jfl simulate: unknown Artifact Type "Tiket". Declared Types: Spec, Ticket, Issue`
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("simulate Tiket: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
	if r = p.Run("simulate"); r.ExitCode != 2 {
		t.Errorf("simulate with no Type: exit %d, want 2 (usage)", r.ExitCode)
	}
}
