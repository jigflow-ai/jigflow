package main_test

import (
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// renameInProgress renames the Ticket Status in-progress to doing in the
// Artifact Type file at rel.
func renameInProgress(p *clitest.Project, rel string) {
	p.Write(rel, strings.ReplaceAll(p.Read(rel), "in-progress", "doing"))
}

// orphaned is the skeleton project with T-1 and T-2 in-progress and T-3
// ready-for-agent, after its Playbook renamed in-progress to doing.
func orphaned(t *testing.T) *clitest.Project {
	t.Helper()
	p := ticketPlaybook(t)
	for _, title := range []string{"Reset-token table", "Reset email", "Rate limit"} {
		p.MustRun("create", "Ticket", "--title", title)
	}
	p.MustRun("move", "T-1", "in-progress")
	p.MustRun("move", "T-2", "in-progress")
	renameInProgress(p, ".jigflow/types/ticket.yaml")
	return p
}

func TestAPlaybookThatOrphansArtifactsFailsToLoadAndListsThem(t *testing.T) {
	p := orphaned(t)
	for _, cmd := range []string{"check", "next"} {
		r := p.Run(cmd)
		for _, want := range []string{
			"the Playbook leaves 2 Artifacts in an undeclared Status:",
			`T-1 "Reset-token table": Ticket has no Status "in-progress"`,
			`T-2 "Reset email": Ticket has no Status "in-progress"`,
		} {
			if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
				t.Errorf("%s: exit %d, stderr %q; want exit 1 and %q", cmd, r.ExitCode, r.Stderr, want)
			}
		}
		if strings.Contains(r.Stderr, "T-3") {
			t.Errorf("%s: stderr %q lists T-3, whose Status is still declared", cmd, r.Stderr)
		}
	}
}

func TestABasePlaybookUpdateThatOrphansArtifactsFailsToLoadAndListsThem(t *testing.T) {
	p := extending(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-1", "in-progress")
	renameInProgress(p, "base/types/ticket.yaml")

	r := p.Run("check")
	want := "the Playbook leaves 1 Artifact in an undeclared Status:\n  " + `T-1 "Reset-token table": Ticket has no Status "in-progress"`
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
}

// renameMigration is a Playbook Migration mapping the Ticket Status
// in-progress to doing.
const renameMigration = "Ticket:\n  in-progress: doing\n"

func TestMigrateMovesEveryAffectedArtifactToItsMappedStatus(t *testing.T) {
	p := orphaned(t)
	p.Write(".jigflow/state/T-1.md", p.Read(".jigflow/state/T-1.md")+"Store reset tokens hashed.\n")
	p.Write(".jigflow/migrations/doing.yaml", renameMigration)

	r := p.MustRun("migrate")
	if want := "T-1: in-progress → doing\nT-2: in-progress → doing\n"; r.Stdout != want {
		t.Errorf("migrate printed %q, want %q", r.Stdout, want)
	}
	for id, want := range map[string]string{"T-1": "doing", "T-2": "doing", "T-3": "ready-for-agent"} {
		if got := frontmatter(t, p.Read(".jigflow/state/"+id+".md"))["status"]; got != want {
			t.Errorf("%s status = %q, want %q", id, got, want)
		}
	}
	if got := p.Read(".jigflow/state/T-1.md"); !strings.HasSuffix(got, "Store reset tokens hashed.\n") {
		t.Errorf("migrate lost T-1's body:\n%s", got)
	}

	// The Playbook loads, and the migrated Artifacts move on from their new
	// Statuses: the Store wrote them, so their frontmatter isn't refused.
	p.MustRun("check")
	if r := p.MustRun("move", "T-1", "in-review"); !strings.Contains(r.Stdout, "T-1: doing → in-review") {
		t.Errorf("move after migrate printed %q", r.Stdout)
	}
}

func TestMigrateChangesNothingWhileAnOrphanHasNoMapping(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("create", "Ticket", "--title", "Audit log")
	p.MustRun("move", "T-1", "in-progress")
	p.MustRun("move", "T-2", "in-progress")
	p.MustRun("move", "T-2", "in-review")
	// Renaming in-review too orphans T-2, which the Migration doesn't map.
	renameInProgress(p, ".jigflow/types/ticket.yaml")
	p.Write(".jigflow/types/ticket.yaml", strings.ReplaceAll(p.Read(".jigflow/types/ticket.yaml"), "in-review", "reviewing"))
	p.Write(".jigflow/migrations/doing.yaml", renameMigration)
	before := p.Read(".jigflow/state/T-1.md")

	r := p.Run("migrate")
	want := "no Playbook Migration maps 1 Artifact:\n  " + `T-2 "Audit log": Ticket has no Status "in-review"`
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("migrate: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused migrate changed T-1:\n%s", after)
	}
}

func TestCheckReportsAPlaybookMigrationToAnUndeclaredStatusOrType(t *testing.T) {
	const file = ".jigflow/migrations/doing.yaml"
	for name, c := range map[string]struct{ migration, want string }{
		"undeclared Status":        {"Ticket:\n  in-progress: doign\n", file + `: the Migration of Ticket "in-progress" names Status "doign", which a Ticket doesn't declare`},
		"undeclared Artifact Type": {"Tikcet:\n  in-progress: doing\n", file + `: a Migration names Artifact Type "Tikcet", which the Playbook doesn't declare`},
	} {
		t.Run(name, func(t *testing.T) {
			p := orphaned(t)
			p.Write(file, c.migration)
			r := p.Run("check")
			if r.ExitCode != 1 || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, c.want)
			}
			if r := p.Run("migrate"); r.ExitCode != 1 || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("migrate: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, c.want)
			}
		})
	}
}

func TestAPlaybookMigrationMigratesArtifactsABasePlaybookUpdateOrphans(t *testing.T) {
	p := extending(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-1", "in-progress")
	renameInProgress(p, "base/types/ticket.yaml")
	p.Write(".jigflow/migrations/doing.yaml", renameMigration)

	if r := p.MustRun("migrate"); r.Stdout != "T-1: in-progress → doing\n" {
		t.Errorf("migrate printed %q", r.Stdout)
	}
	p.MustRun("check")
	if got := firstLine(p.MustRun("next").Stdout); got != `run /implement on T-1 "Reset-token table"` {
		t.Errorf("next = %q, want the Base Playbook's Binding of doing", got)
	}
}

func TestMigrateRefusesAnArtifactWhoseFrontmatterWasEditedOutsideJfl(t *testing.T) {
	p := orphaned(t)
	p.Write(".jigflow/migrations/doing.yaml", renameMigration)
	p.Write(".jigflow/state/T-2.md", strings.Replace(p.Read(".jigflow/state/T-2.md"), "title: Reset email", "title: Reset mail", 1))
	before := p.Read(".jigflow/state/T-1.md")

	r := p.Run("migrate")
	if want := "T-2: its frontmatter was changed outside jfl"; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("migrate: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("a refused migrate changed T-1:\n%s", after)
	}
}

func TestTheOrphanedArtifactsSayHowToMigrateThem(t *testing.T) {
	p := orphaned(t)
	hint := "map their Statuses to declared ones in a Playbook Migration (.jigflow/migrations/*.yaml), then run jfl migrate"
	if r := p.Run("next"); !strings.Contains(r.Stderr, hint) {
		t.Errorf("next: stderr %q; want %q", r.Stderr, hint)
	}

	p.Write(".jigflow/migrations/doing.yaml", renameMigration)
	want := `T-1 "Reset-token table": Ticket has no Status "in-progress", which a Playbook Migration maps to "doing"` + "\n"
	if r := p.Run("next"); r.ExitCode != 1 || !strings.Contains(r.Stderr, want) || !strings.Contains(r.Stderr, "run jfl migrate") {
		t.Errorf("next: exit %d, stderr %q; want exit 1, %q and to run jfl migrate", r.ExitCode, r.Stderr, want)
	}
}

func TestMigratingIntoAStatusWithNoBindingReleasesTheClaimAndFocus(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.RunInSession("A", "next")
	if r := p.RunInSession("A", "move", "T-1", "in-progress"); r.ExitCode != 0 {
		t.Fatalf("agent A's move exited %d: %s", r.ExitCode, r.Stderr)
	}
	renameInProgress(p, ".jigflow/types/ticket.yaml")
	p.Write(".jigflow/migrations/review.yaml", "Ticket:\n  in-progress: in-review\n")

	p.MustRun("migrate")
	if got := claimOf(t, p.Read(".jigflow/state/T-1.md")); got != "" {
		t.Errorf("T-1 is claimed by %q in in-review, which has no Binding; want no Claim", got)
	}
	if got := focusOf(t, p, "A"); got != "" {
		t.Errorf("agent A's Focus = %q after T-1 became human work, want none", got)
	}
}

func TestOnlyAPersonCanMigrate(t *testing.T) {
	p := orphaned(t)
	p.Write(".jigflow/migrations/doing.yaml", renameMigration)
	before := p.Read(".jigflow/state/T-1.md")

	r := p.RunInSession("A", "migrate")
	if want := "Only a human can migrate Artifacts."; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("migrate by an agent: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
	if after := p.Read(".jigflow/state/T-1.md"); after != before {
		t.Errorf("an agent's migrate changed T-1:\n%s", after)
	}
}
