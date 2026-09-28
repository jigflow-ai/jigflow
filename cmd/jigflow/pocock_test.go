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
	for _, l := range []string{"spec", "ticket", "issue", "bug", "enhancement", "ready-for-agent", "ticketed", "in-progress", "in-review", "ready-to-merge", "needs-triage", "needs-info", "ready-for-human", "wontfix",
		"wayfinder:map", "wayfinder:ticket", "wayfinder:research", "wayfinder:prototype", "wayfinder:grilling", "wayfinder:task", "open", "dropped", "out-of-scope"} {
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
		"Map": {"1. open → cleared"},
		"Decision Ticket": {
			"1. open → in-progress → resolved",
			"2. open → in-progress → dropped",
			"3. open → in-progress → out-of-scope",
			"4. open → dropped",
			"5. open → out-of-scope",
		},
		"Out of Scope": {"1. ruled-out"},
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
	for _, name := range []string{"triage", "to-spec", "to-tickets", "implement", "code-review", "wayfinder", "resolve-decision", "domain-modeling"} {
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

// chartedMap is a Pocock project with a Map a person approved as charted:
// MAP-41, with the Decision Tickets D-42 (research), D-43 (grilling) and
// D-44 (prototype), which D-42 blocks.
func chartedMap(t *testing.T) (*clitest.Project, *fakegithub.Server) {
	t.Helper()
	p, gh := pocock(t)
	p.Write("map.yaml", `summary: chart the way to a password-reset Spec
items:
  - {create: Map, ref: map, title: Password reset}
  - {create: Decision Ticket, ref: tokens, title: "How long does a reset token live?", fields: {kind: research}, links: {part_of: [map]}}
  - {create: Decision Ticket, title: "Which provider sends the email?", fields: {kind: grilling}, links: {part_of: [map]}}
  - {create: Decision Ticket, title: "What does the reset page ask for?", fields: {kind: prototype}, links: {part_of: [map], blocked_by: [tokens]}}
`)
	if r := p.RunInSession("A", "propose", "map.yaml"); r.ExitCode != 0 {
		t.Fatalf("proposing the Map exited %d: %s", r.ExitCode, r.Stderr)
	}
	approveInTerminal(t, p, "P-1")
	return p, gh
}

func TestWayfinderChartsAMapOfDecisionTicketsAPersonApproves(t *testing.T) {
	p, gh := chartedMap(t)

	if i := gh.Issue(t, 41); !slices.Contains(i.Labels, "wayfinder:map") || i.State != "open" {
		t.Errorf("issue 41 should be the open Map, labelled wayfinder:map: %+v", i)
	}
	if i := gh.Issue(t, 42); !slices.Contains(i.Labels, "wayfinder:ticket") || !slices.Contains(i.Labels, "wayfinder:research") {
		t.Errorf("issue 42 should be a research Decision Ticket: %+v", i)
	}
	if i := gh.Issue(t, 44); fmt.Sprint(i.BlockedBy) != "[42]" {
		t.Errorf("issue 44 should be blocked by 42, natively: %+v", i)
	}
	if r := p.RunInSession("A", "create", "Decision Ticket", "--title", "Do we rate-limit resets?"); r.ExitCode != 1 {
		t.Errorf("an agent creating a Decision Ticket without a Proposal exited %d, want it refused: %s", r.ExitCode, r.Stdout)
	}
	if r := agentMove(t, p, "D-43", "out-of-scope", 1); !strings.Contains(r.Stderr, "Human Transition") {
		t.Errorf("an agent ruling a Decision Ticket out of scope should be refused as a Human Transition: %s", r.Stderr)
	}
	if r := agentMove(t, p, "MAP-41", "cleared", 1); !strings.Contains(r.Stderr, "Human Transition") {
		t.Errorf("an agent clearing the Map should be refused as a Human Transition: %s", r.Stderr)
	}
}

func TestParallelSessionsWorkAMapThroughClaims(t *testing.T) {
	p, gh := chartedMap(t)

	if first, _ := agentNext(t, p); first != `run /resolve-decision on D-42 "How long does a reset token live?"` {
		t.Fatalf("session A's next = %q, want the first Decision Ticket on the frontier", first)
	}
	agentMove(t, p, "D-42", "in-progress", 0)
	if i := gh.Issue(t, 42); len(i.Assignees) != 1 {
		t.Errorf("session A's Claim on D-42 should assign issue 42: %+v", i)
	}

	r := p.RunInSession("B", "next")
	for _, want := range []string{
		`run /resolve-decision on D-43 "Which provider sends the email?"`,
		"D-42: claimed by agent session A",
		`D-44: not ready: waiting until every "blocked_by" item is resolved/dropped/out-of-scope`,
		"MAP-41: \"open\" has no Binding, so it's human work",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("session B's next should say %q:\n%s", want, r.Stdout)
		}
	}
	if r := p.RunInSession("B", "move", "D-42", "resolved"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "D-42 is claimed by agent session A.") {
		t.Errorf("session B resolving A's D-42 exited %d: %s", r.ExitCode, r.Stderr)
	}
	if r := p.RunInSession("B", "move", "D-44", "in-progress"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "not ready") {
		t.Errorf("session B taking the blocked D-44 exited %d: %s", r.ExitCode, r.Stderr)
	}

	p.RunInSession("A", "comment", "D-42", "Decided: a reset token lives one hour.")
	agentMove(t, p, "D-42", "resolved", 0)
	if i := gh.Issue(t, 42); i.State != "closed" || len(i.Assignees) != 0 {
		t.Errorf("resolving D-42 should close issue 42 and release the Claim: %+v", i)
	}
	if r := p.RunInSession("B", "move", "D-44", "in-progress"); r.ExitCode != 0 {
		t.Errorf("once D-42 is resolved, session B should take D-44; exited %d: %s", r.ExitCode, r.Stderr)
	}
}

func TestAnOutOfScopeRecordIsAFileOnlyThePersonReconsiders(t *testing.T) {
	p, _ := pocock(t)

	if r := p.RunInSession("A", "create", "Out of Scope", "--title", "Dark mode"); r.ExitCode != 0 {
		t.Fatalf("an agent recording a rejected concept exited %d: %s", r.ExitCode, r.Stderr)
	}
	if got := frontmatter(t, p.Read(".jigflow/state/OOS-1.md"))["status"]; got != "ruled-out" {
		t.Errorf("OOS-1 status = %q, want ruled-out", got)
	}
	if r := agentMove(t, p, "OOS-1", "reconsidered", 1); !strings.Contains(r.Stderr, "Human Transition") {
		t.Errorf("an agent reconsidering OOS-1 should be refused as a Human Transition: %s", r.Stderr)
	}
	if _, all := agentNext(t, p); strings.Contains(all, "OOS-1") {
		t.Errorf("an Out of Scope record is no one's work, so next should leave it out:\n%s", all)
	}
}

// upstreamSkills is the plugin manifest of mattpocock/skills at the commit
// the Playbook is pinned to: every skill it ships, by its path upstream.
var upstreamSkills = []string{
	"engineering/ask-matt", "engineering/diagnosing-bugs", "engineering/grill-with-docs",
	"engineering/triage", "engineering/improve-codebase-architecture",
	"engineering/setup-matt-pocock-skills", "engineering/tdd", "engineering/to-spec",
	"engineering/to-tickets", "engineering/wayfinder", "engineering/implement",
	"engineering/prototype", "engineering/research", "engineering/domain-modeling",
	"engineering/codebase-design", "engineering/code-review",
	"engineering/resolving-merge-conflicts", "engineering/wizard",
	"productivity/grill-me", "productivity/grilling", "productivity/handoff",
	"productivity/teach", "productivity/to-questionnaire", "productivity/wait-what",
	"productivity/writing-for-agents",
}

// Every skill upstream ships is a Skill of the Playbook, started the way
// upstream means it to be: by a person only where upstream turns model
// invocation off, through a Binding where the Playbook's topology hands it
// out, and by the agent otherwise. setup-matt-pocock-skills and ask-matt
// are replaced by jfl init and the router.
func TestEverySkillUpstreamShipsIsPublishedWithItsInvocationMode(t *testing.T) {
	p, _ := pocock(t)

	modes := map[string]string{
		"triage": "user", "to-spec": "user", "wayfinder": "user", "grill-me": "user",
		"grill-with-docs": "user", "handoff": "user", "improve-codebase-architecture": "user",
		"teach": "user", "to-questionnaire": "user", "wait-what": "user",
		"tdd": "agent", "diagnosing-bugs": "agent", "prototype": "agent", "research": "agent",
		"domain-modeling": "agent", "codebase-design": "agent", "resolving-merge-conflicts": "agent",
		"wizard": "agent", "grilling": "agent", "writing-for-agents": "agent",
		"to-tickets": "bound", "implement": "bound", "code-review": "bound", "resolve-decision": "bound",
	}
	for _, path := range upstreamSkills {
		name := filepath.Base(path)
		if _, ok := modes[name]; !ok && name != "ask-matt" && name != "setup-matt-pocock-skills" {
			t.Errorf("upstream's %s has no expected Invocation Mode in this test", name)
		}
	}
	for name, mode := range modes {
		content, err := os.ReadFile(filepath.Join(p.Dir, ".claude/skills", name, "SKILL.md"))
		if err != nil {
			t.Errorf("the %s Skill wasn't published: %v", name, err)
			continue
		}
		fm, _ := skillFrontmatter(t, string(content))
		desc, _ := fm["description"].(string)
		var got string
		switch {
		case fm["disable-model-invocation"] == true:
			got = "user"
		case strings.Contains(desc, "Run it when jfl next names it"):
			got = "bound"
		default:
			got = "agent"
		}
		if got != mode || desc == "" {
			t.Errorf("the %s Skill is published as %s with description %q, want %s and a description", name, got, desc, mode)
		}
	}
	for _, replaced := range []string{"ask-matt", "setup-matt-pocock-skills"} {
		if _, err := os.Stat(filepath.Join(p.Dir, ".claude/skills", replaced)); err == nil {
			t.Errorf("%s is replaced, so it shouldn't be published", replaced)
		}
	}
	_, router := skillFrontmatter(t, p.Read(".claude/skills/jigflow/SKILL.md"))
	for _, want := range []string{"- /wayfinder: ", "- /triage: ", "- /teach: ", "## Decision Ticket\n\n- open: /resolve-decision\n"} {
		if !strings.Contains(router, want) {
			t.Errorf("the router, standing in for ask-matt, should say %q:\n%s", want, router)
		}
	}
}

// The sync script reads UPSTREAM: each skill upstream ships is either
// adapted, from its SKILL.md, into a file the Playbook has, or replaced.
func TestUpstreamRecordsWhereEverySkillUpstreamShipsWent(t *testing.T) {
	dir := pocockDir(t)
	content, err := os.ReadFile(filepath.Join(dir, "UPSTREAM"))
	if err != nil {
		t.Fatal(err)
	}
	adapted, replaced := map[string]bool{}, map[string]bool{}
	for n, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, " ")
		switch {
		case f[0] == "repo" && len(f) == 2, f[0] == "commit" && len(f) == 2:
		case f[0] == "adapted" && len(f) == 3:
			if _, err := os.Stat(filepath.Join(dir, f[1])); err != nil {
				t.Errorf("UPSTREAM line %d: %s isn't in the Playbook: %v", n+1, f[1], err)
			}
			adapted[f[2]] = true
		case f[0] == "replaced" && len(f) >= 3:
			replaced[f[1]] = true
		default:
			t.Errorf("UPSTREAM line %d isn't a record: %q", n+1, line)
		}
	}
	for _, path := range upstreamSkills {
		if !adapted["skills/"+path+"/SKILL.md"] && !replaced["skills/"+path] {
			t.Errorf("UPSTREAM doesn't record where upstream's %s went", path)
		}
	}
	for _, path := range []string{"engineering/setup-matt-pocock-skills", "engineering/ask-matt"} {
		if !replaced["skills/"+path] {
			t.Errorf("UPSTREAM should record %s as replaced", path)
		}
	}
}
