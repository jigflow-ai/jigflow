package main_test

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
)

// pocockRepository points the project's jfl init's Pocock offer at a Base
// Playbook in a git repository of the test's own, tagged v1, as
// JFL_POCOCK_GIT and JFL_POCOCK_REF let a fork or a mirror stand in for the
// published one.
func pocockRepository(t *testing.T, p *clitest.Project) *upstream {
	t.Helper()
	u := gitUpstream(t, bin.NewProject(t))
	p.Setenv("JFL_POCOCK_GIT", u.URL)
	p.Setenv("JFL_POCOCK_REF", "v1")
	return u
}

func TestInitSetsAProjectUpExtendingThePocockPlaybookFromItsRepository(t *testing.T) {
	p := bin.NewProject(t)
	u := pocockRepository(t, p)

	r := p.MustRun("init", "--playbook", "pocock", "--adapter", "agents-md")
	if !strings.Contains(r.Stdout, "extending the Pocock Playbook") {
		t.Errorf("init output:\n%s", r.Stdout)
	}
	if got, want := p.Read(".jigflow/playbook.yaml"), "extends: {git: "+u.URL+", ref: v1}\n"; !strings.Contains(got, want) {
		t.Errorf("the Playbook file should extend the Pocock Playbook's repository with %q:\n%s", want, got)
	}
	if got := lock(t, p)["commit"]; got != u.git("rev-parse", "v1") {
		t.Errorf("the lockfile pins %q, want v1's commit", got)
	}
	p.MustRun("check")
}

func TestInitLeavesTheProjectAsItWasWhenThePocockPlaybookCantBeFetched(t *testing.T) {
	p := bin.NewProject(t)
	p.Setenv("JFL_POCOCK_GIT", "file://"+p.Dir+"/nowhere")
	p.Setenv("JFL_POCOCK_REF", "v1")

	r := p.Run("init", "--playbook", "pocock", "--adapter", "agents-md")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "nowhere@v1") {
		t.Errorf("init exited %d: %s", r.ExitCode, r.Stderr)
	}
	if files := tree(t, p); len(files) != 0 {
		t.Errorf("a failed init left %v", files)
	}
}

// pocockDir is the Pocock Playbook's repository: JFL_POCOCK_PLAYBOOK, or a
// checkout of jigflow-playbook-pocock next to this repository's. A test
// needing it is skipped without one, since it lives in a repository of its
// own (ADR 0009).
func pocockDir(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	dir := os.Getenv("JFL_POCOCK_PLAYBOOK")
	if dir == "" {
		dir, _ = filepath.Abs(filepath.Join("..", "..", "..", "jigflow-playbook-pocock"))
	}
	if _, err := os.Stat(filepath.Join(dir, "playbook.yaml")); err != nil {
		t.Skipf("needs the Pocock Playbook's repository: set JFL_POCOCK_PLAYBOOK, or check it out next to this one (%v)", err)
	}
	return dir
}

// pocock is a project jfl init set up with the Pocock Playbook, fetched from
// the main branch of its repository, keeping its Specs, Tickets and Issues
// in the fake GitHub's acme/shop, whose issues are numbered from 41.
func pocock(t *testing.T) (*clitest.Project, *fakegithub.Server) {
	t.Helper()
	dir := pocockDir(t)
	gh := fakegithub.New(t, 41)
	for _, l := range []string{"spec", "ticket", "issue", "bug", "enhancement", "ready-for-agent", "ticketed", "in-progress", "in-review", "ready-to-merge", "needs-triage", "needs-info", "ready-for-human", "wontfix"} {
		gh.AddLabel(l)
	}
	t.Setenv("GH_TOKEN", fakegithub.Token)
	t.Setenv("GITHUB_API_URL", gh.URL)
	p := bin.NewProject(t)
	p.Setenv("PATH", filepath.Dir(githubConnector)+string(os.PathListSeparator)+os.Getenv("PATH"))
	p.Setenv("JFL_POCOCK_GIT", "file://"+dir)
	p.Setenv("JFL_POCOCK_REF", "main")
	r := p.MustRun("init", "--playbook", "pocock", "--adapter", "claude-code", "--setting", "github.repo=acme/shop")
	if !strings.Contains(r.Stdout, "extending the Pocock Playbook") {
		t.Fatalf("init output:\n%s", r.Stdout)
	}
	return p, gh
}

// The CI run of every shipped Playbook: it passes check, and simulate walks
// each of its Artifact Types.
func TestThePocockPlaybookPassesCheckAndSimulatesEveryType(t *testing.T) {
	p, _ := pocock(t)

	if r := p.MustRun("check"); firstLine(r.Stdout) != fmt.Sprintf("Playbook %q: no problems", filepath.Base(p.Dir)) {
		t.Errorf("check = %q", r.Stdout)
	}
	for typ, want := range map[string][]string{
		"Spec":   {"1. ready-for-agent → ticketed"},
		"Ticket": {"1. ready-for-agent → in-progress → in-review → ready-to-merge → done"},
		"Issue": {
			"1. needs-triage → needs-info → wontfix",
			"2. needs-triage → ready-for-agent → ready-for-human → done",
			"3. needs-triage → ready-for-agent → in-progress → in-review → ready-to-merge → done",
			"4. needs-triage → ready-for-human → ready-for-agent → in-progress → in-review → ready-to-merge → done",
			"5. needs-triage → ready-for-human → done",
			"6. needs-triage → wontfix",
		},
		"ADR": {"1. proposed → accepted"},
	} {
		r := p.MustRun("simulate", typ)
		if got := pathLines(r.Stdout); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("simulate %s paths =\n%s\nwant\n%s", typ, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	ticket := p.MustRun("simulate", "Ticket").Stdout
	for _, want := range []string{
		`ready-for-agent: Skill /implement; Readiness: every "blocked_by" item is done`,
		"→ in-review; Gates: tests (no command yet), lint (no command yet)",
		"in-review: Skill /code-review",
		"ready-to-merge: no Binding, human work",
		"→ done; Human Transition; Actions: commit",
	} {
		if !strings.Contains(ticket, want) {
			t.Errorf("simulate Ticket should show %q:\n%s", want, ticket)
		}
	}
}

func TestToTicketsBreaksASpecIntoTicketsAsOneProposal(t *testing.T) {
	p, gh := pocock(t)
	p.Write("spec.yaml", "summary: spec for password reset\nitems:\n  - {create: Spec, title: Password reset by email}\n")
	p.RunInSession("A", "propose", "spec.yaml")
	approveInTerminal(t, p, "P-1")
	if i := gh.Issue(t, 41); !slices.Contains(i.Labels, "spec") || !slices.Contains(i.Labels, "ready-for-agent") {
		t.Fatalf("the approved Spec should be issue 41, labelled spec and ready-for-agent: %+v", i)
	}
	if first, _ := agentNext(t, p); first != `run /to-tickets on SPEC-41 "Password reset by email"` {
		t.Fatalf("next = %q, want /to-tickets on SPEC-41", first)
	}

	p.Write("tickets.yaml", `summary: break SPEC-41 into 2 Tickets
items:
  - {create: Ticket, ref: table, title: Reset-token table, links: {part_of: [SPEC-41]}}
  - {create: Ticket, title: Request a reset link by email, links: {part_of: [SPEC-41], blocked_by: [table]}}
  - {move: SPEC-41, to: ticketed}
`)
	if r := p.RunInSession("A", "propose", "tickets.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d: %s", r.ExitCode, r.Stderr)
	}
	if got := p.MustRun("query", "--type", "Ticket").Stdout; got != "no Artifacts\n" || slices.Contains(gh.Issue(t, 41).Labels, "ticketed") {
		t.Fatalf("nothing should change before a person approves the breakdown; Tickets:\n%s", got)
	}
	approveInTerminal(t, p, "P-2")
	if i := gh.Issue(t, 43); !slices.Contains(i.Labels, "ticket") || fmt.Sprint(i.BlockedBy) != "[42]" {
		t.Errorf("issue 43 should be a Ticket blocked by 42: %+v", i)
	}
	if i := gh.Issue(t, 41); !slices.Contains(i.Labels, "ticketed") {
		t.Errorf("the Spec should be ticketed: %+v", i)
	}
	if first, all := agentNext(t, p); first != `run /implement on T-42 "Reset-token table"` || !strings.Contains(all, `T-43: not ready: waiting until every "blocked_by" item is done`) {
		t.Errorf("next should offer T-42 and hold T-43 back:\n%s", all)
	}
}

func TestAnAgentFilesAnIssueIntoNeedsTriageButOnlyThePersonDecidesWhereItGoes(t *testing.T) {
	p, gh := pocock(t)

	if r := p.RunInSession("A", "create", "Issue", "--title", "Token not invalidated after use", "--field", "category=bug"); r.ExitCode != 0 {
		t.Fatalf("an agent filing an Issue exited %d: %s", r.ExitCode, r.Stderr)
	}
	if i := gh.Issue(t, 41); fmt.Sprint(i.Labels) != "[needs-triage bug issue]" {
		t.Errorf("issue 41 is labelled %v, want needs-triage, bug and issue", i.Labels)
	}
	if first, _ := agentNext(t, p); !strings.HasPrefix(first, "nothing") {
		t.Errorf("an Issue in needs-triage is the maintainer's, not an agent's: next = %q", first)
	}
	if r := agentMove(t, p, "I-41", "ready-for-agent", 1); !strings.Contains(r.Stderr, "Human Transition") {
		t.Errorf("an agent deciding an Issue is ready should be refused as a Human Transition: %s", r.Stderr)
	}
	agentMove(t, p, "I-41", "needs-info", 0)
	agentMove(t, p, "I-41", "needs-triage", 0)
	humanMove(t, p, "I-41", "wontfix")
	if i := gh.Issue(t, 41); i.State != "closed" || !slices.Contains(i.Labels, "wontfix") {
		t.Errorf("wontfix should close issue 41 with the wontfix label: %+v", i)
	}
}

func TestThePocockPlaybooksSkillsUseJflNeverATrackersCLI(t *testing.T) {
	p, _ := pocock(t)

	skills := map[string]string{}
	for rel, content := range tree(t, p) {
		if dir, rest, ok := strings.Cut(rel, ".claude/skills/"); ok && dir == "" && strings.HasSuffix(rest, "/SKILL.md") {
			skills[strings.TrimSuffix(rest, "/SKILL.md")] = content
		}
	}
	for _, name := range []string{"triage", "to-spec", "to-tickets", "implement", "tdd", "code-review"} {
		if _, ok := skills[name]; !ok {
			t.Errorf("the %s Skill wasn't published; published: %v", name, slices.Sorted(maps.Keys(skills)))
		}
	}
	tracker := regexp.MustCompile("(^|[^[:alnum:]_-])(gh|glab) ")
	for name, content := range skills {
		if loc := tracker.FindStringIndex(content); loc != nil {
			t.Errorf("the %s Skill uses a tracker's CLI: %q", name, content[max(0, loc[0]-40):min(len(content), loc[1]+40)])
		}
	}
	for _, name := range []string{"triage", "to-spec", "to-tickets", "implement", "code-review"} {
		if !strings.Contains(skills[name], "jfl ") {
			t.Errorf("the %s Skill should work through jfl", name)
		}
	}
}

func TestThePocockPlaybookCarriesTheUpstreamNoticeAndCommit(t *testing.T) {
	dir := pocockDir(t)
	license, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil || !strings.Contains(string(license), "MIT License") || !strings.Contains(string(license), "Copyright (c) 2026 Matt Pocock") {
		t.Errorf("LICENSE should be MIT, © 2026 Matt Pocock (%v):\n%s", err, license)
	}
	upstream, err := os.ReadFile(filepath.Join(dir, "UPSTREAM"))
	if err != nil || !regexp.MustCompile(`(?m)^repo https://github\.com/mattpocock/skills\ncommit [0-9a-f]{40}$`).Match(upstream) {
		t.Errorf("UPSTREAM should record the upstream repository and the commit it is pinned to (%v):\n%s", err, upstream)
	}
}
