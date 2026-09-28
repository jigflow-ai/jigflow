package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"go.yaml.in/yaml/v3"
)

// linkedPlaybook is a small Pocock-style Playbook: a Ticket is part_of a Spec
// and may be blocked_by other Tickets. A Ticket is ready for an agent only when
// every blocked_by Ticket is done, and a Spec can't be ticketed until at least
// one Ticket is part_of it. Starting a Ticket and ticketing a Spec each run a
// Gate that records itself in ran.log.
func linkedPlaybook(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: linked\n")
	p.Write(".jigflow/types/spec.yaml", `name: Spec
prefix: S
statuses: [ready-for-agent, ticketed]
initial: [ready-for-agent]
final: [ticketed]
bindings:
  ready-for-agent: to-tickets
transitions:
  - from: ready-for-agent
    to: ticketed
    guards:
      - {kind: has-incoming, link: part_of, min: 1}
    gates:
      - {name: ticketed, cmd: sh record.sh ticketed}
`)
	p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
statuses: [ready-for-agent, in-progress, done]
initial: [ready-for-agent]
final: [done]
links:
  blocked_by: Ticket
  part_of: Spec
bindings:
  ready-for-agent: implement
  in-progress: implement
readiness:
  ready-for-agent:
    - {kind: linked-all-in, link: blocked_by, statuses: [done]}
transitions:
  - from: ready-for-agent
    to: in-progress
    gates:
      - {name: start, cmd: sh record.sh start}
  - from: in-progress
    to: done
`)
	p.Write("record.sh", record)
	return p
}

// links parses the links field of an Artifact file's frontmatter.
func links(t *testing.T, content string) map[string][]string {
	t.Helper()
	rest, _ := strings.CutPrefix(content, "---\n")
	fm, _, _ := strings.Cut(rest, "\n---\n")
	var m struct {
		Links map[string][]string `yaml:"links"`
	}
	if err := yaml.Unmarshal([]byte(fm), &m); err != nil {
		t.Fatalf("frontmatter is not YAML: %v\n%s", err, fm)
	}
	return m.Links
}

func TestCreateRecordsLinksToOtherArtifacts(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset")
	p.MustRun("create", "Ticket", "--title", "Reset-token table", "--link", "part_of=S-1")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "part_of=S-1", "--link", "blocked_by=T-1")

	got := links(t, p.Read(".jigflow/state/T-2.md"))
	if strings.Join(got["part_of"], ",") != "S-1" || strings.Join(got["blocked_by"], ",") != "T-1" {
		t.Errorf("T-2 links = %v, want part_of [S-1] and blocked_by [T-1]", got)
	}
}

func TestCreateRefusesAnUndeclaredLinkName(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")

	r := p.Run("create", "Ticket", "--title", "Reset endpoint", "--link", "depends_on=T-1")
	if r.ExitCode != 1 {
		t.Fatalf("create with an undeclared Link exited %d, want 1", r.ExitCode)
	}
	for _, want := range []string{`"depends_on"`, "blocked_by", "part_of"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("refusal %q should mention %s (the bad name and the declared Links)", r.Stderr, want)
		}
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".jigflow/state/T-2.md")); err == nil {
		t.Error("a refused create wrote an Artifact file")
	}
}

func TestCreateRefusesALinkToAnArtifactThatDoesNotExist(t *testing.T) {
	p := linkedPlaybook(t)

	r := p.Run("create", "Ticket", "--title", "Reset endpoint", "--link", "blocked_by=T-7")
	if r.ExitCode != 1 {
		t.Fatalf("create with a Link to a missing Artifact exited %d, want 1", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "T-7") {
		t.Errorf("refusal %q should name the missing Artifact", r.Stderr)
	}
}

func TestCreateRefusesALinkToAnArtifactOfTheWrongType(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")

	r := p.Run("create", "Ticket", "--title", "Reset endpoint", "--link", "part_of=T-1")
	if r.ExitCode != 1 {
		t.Fatalf("create with part_of pointing at a Ticket exited %d, want 1", r.ExitCode)
	}
	for _, want := range []string{"T-1", `"part_of"`, "Spec"} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("refusal %q should mention %s", r.Stderr, want)
		}
	}
}

func TestNextSkipsAnArtifactWhoseReadinessFailsAndSaysWhatItIsWaitingFor(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "blocked_by=T-1")
	p.MustRun("move", "T-1", "in-progress")

	r := p.MustRun("next")
	if first := firstLine(r.Stdout); first != `run /implement on T-1 "Reset-token table"` {
		t.Errorf("next = %q, want T-1", first)
	}
	if want := `T-2: not ready: waiting until every "blocked_by" item is done`; !strings.Contains(r.Stdout, want) {
		t.Errorf("next output should contain %q:\n%s", want, r.Stdout)
	}

	p.MustRun("move", "T-1", "done")
	r = p.MustRun("next")
	if first := firstLine(r.Stdout); first != `run /implement on T-2 "Reset endpoint"` {
		t.Errorf("once T-1 is done, next = %q, want T-2", first)
	}
	if strings.Contains(r.Stdout, "not ready") {
		t.Errorf("T-2 is ready now, but next still says:\n%s", r.Stdout)
	}
}

func TestAnAgentMovingOutOfAStatusWhoseReadinessFailsIsRefusedBeforeAnyGateRuns(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "blocked_by=T-1")
	before := p.Read(".jigflow/state/T-2.md")

	r := p.RunInSession("A", "move", "T-2", "in-progress")
	if r.ExitCode != 1 {
		t.Fatalf("move of a not-ready Artifact exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	want := `T-2: not ready. Readiness of "ready-for-agent" needs every "blocked_by" item is done`
	if !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/T-2.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "ran.log")); err == nil {
		t.Errorf("the Gate ran although Readiness refused the move: %q", p.Read("ran.log"))
	}
}

func TestAPersonMayMoveOutOfAStatusWhoseReadinessFails(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--link", "blocked_by=T-1")

	p.MustRun("move", "T-2", "in-progress")
	if got := p.Read(".jigflow/state/T-2.md"); !strings.Contains(got, "\nstatus: in-progress\n") {
		t.Errorf("T-2 should be in-progress: Readiness applies only to agents\n%s", got)
	}
}

func TestAFailingGuardRefusesTheTransitionAndNamesTheUnmetCondition(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset")
	before := p.Read(".jigflow/state/S-1.md")

	r := p.Run("move", "S-1", "ticketed")
	if r.ExitCode != 1 {
		t.Fatalf("move with a failing Guard exited %d, want 1; stdout: %s", r.ExitCode, r.Stdout)
	}
	want := `S-1: "ready-for-agent" → "ticketed" refused: a Guard needs at least 1 item(s) link here via "part_of"`
	if !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/S-1.md"); after != before {
		t.Errorf("a refused move changed the Artifact file:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "ran.log")); err == nil {
		t.Errorf("the Gate ran although a Guard refused the move: %q", p.Read("ran.log"))
	}

	p.MustRun("create", "Ticket", "--title", "Reset-token table", "--link", "part_of=S-1")
	p.MustRun("move", "S-1", "ticketed")
	if got := frontmatter(t, p.Read(".jigflow/state/S-1.md"))["status"]; got != "ticketed" {
		t.Errorf("once a Ticket is part_of S-1, status = %q, want ticketed", got)
	}
}

func TestGuardsDoNotAffectNext(t *testing.T) {
	p := linkedPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset") // its only Transition's Guard fails

	r := p.MustRun("next")
	if first := firstLine(r.Stdout); first != `run /to-tickets on S-1 "Password reset"` {
		t.Errorf("next = %q, want S-1: a Guard describes finishing, not starting", first)
	}
	if strings.Contains(r.Stdout, "S-1:") {
		t.Errorf("next should not skip S-1 over a Guard:\n%s", r.Stdout)
	}
}

func TestAPlaybookReferringToUndeclaredLinksOrTypesDoesNotLoad(t *testing.T) {
	cases := []struct {
		name, ticket, want string
	}{
		{"Link to an undeclared Type", `links:
  part_of: Epic
`, `Link "part_of" points to Artifact Type "Epic"`},
		{"Readiness on an undeclared Link", `readiness:
  ready-for-agent:
    - {kind: linked-all-in, link: depends_on, statuses: [done]}
`, `"depends_on"`},
		{"Guard on an undeclared incoming Link", `transitions:
  - from: ready-for-agent
    to: done
    guards:
      - {kind: has-incoming, link: reviewed_by, min: 1}
`, `"reviewed_by"`},
		{"condition of an unknown kind", `readiness:
  ready-for-agent:
    - {kind: all-done, link: blocked_by}
`, `"all-done"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := bin.NewProject(t)
			p.Write(".jigflow/playbook.yaml", "name: broken\n")
			p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
statuses: [ready-for-agent, done]
initial: [ready-for-agent]
final: [done]
`+c.ticket)
			r := p.Run("next")
			if r.ExitCode != 1 {
				t.Fatalf("next exited %d, want 1 (the Playbook shouldn't load)", r.ExitCode)
			}
			if !strings.Contains(r.Stderr, c.want) {
				t.Errorf("refusal %q should contain %q", r.Stderr, c.want)
			}
		})
	}
}
