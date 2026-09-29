package main_test

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// noteType is an Artifact Type file, as a Proposal carries it, whose
// written Status is bound to the write-note Skill.
const noteType = `name: Note
prefix: N
statuses: [draft, written, kept]
initial: [draft]
final: [kept]
bindings:
  draft: write-note
transitions:
  - {from: draft, to: written}
  - {from: written, to: kept, human: true}
`

const writeNote = `---
description: Write the Note in Focus.
changes: true
invocation: bound
---
Write the Note, then jfl move it to written.
`

// indented indents every line of s by n spaces, for a YAML block scalar.
func indented(s string, n int) string {
	pad := strings.Repeat(" ", n)
	return pad + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+pad) + "\n"
}

// typeProposal is a Proposal file declaring the Artifact Type name, whose
// file is typeYAML, and the Skills given, by name, with their SKILL.md.
func typeProposal(summary, name, typeYAML string, skills map[string]string) string {
	s := "summary: " + summary + "\nitems:\n  - type: " + name + "\n    text: |\n" + indented(typeYAML, 6)
	for skill, text := range skills {
		s += "  - skill: " + skill + "\n    text: |\n" + indented(text, 6)
	}
	return s
}

func TestAProposalDeclaresAnArtifactTypeAndItsSkillsWhichApprovingWrites(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: own\n")
	p.Write("author.yaml", typeProposal("a Playbook for notes", "Note", noteType, map[string]string{"write-note": writeNote}))

	r := p.RunInSession("A", "propose", "author.yaml")
	if r.ExitCode != 0 {
		t.Fatalf("proposing a Type and its Skill exited %d: %s", r.ExitCode, r.Stderr)
	}
	for _, want := range []string{`declare Artifact Type "Note"`, `write Skill "write-note"`} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("propose should list %q:\n%s", want, r.Stdout)
		}
	}
	if r := p.Run("simulate", "Note"); r.ExitCode != 1 {
		t.Errorf("the Playbook shouldn't have a Note before a person approves: simulate exited %d", r.ExitCode)
	}

	if r := p.MustRun("check", "--proposal", "P-1"); r.Stdout != "Playbook \"own\", as P-1 would make it: no problems\n" {
		t.Errorf("check --proposal = %q", r.Stdout)
	}
	sim := p.MustRun("simulate", "Note", "--proposal", "P-1").Stdout
	if got := strings.Join(pathLines(sim), "\n"); got != "1. draft → written → kept" {
		t.Errorf("simulate Note --proposal P-1 paths = %q:\n%s", got, sim)
	}
	if !strings.Contains(sim, "draft: Skill /write-note") {
		t.Errorf("simulate should show the proposed Binding:\n%s", sim)
	}

	approveInTerminal(t, p, "P-1")
	if got := p.Read(".jigflow/types/note.yaml"); got != noteType {
		t.Errorf("the approved Type's file =\n%s\nwant\n%s", got, noteType)
	}
	if got := p.Read(".jigflow/skills/write-note/SKILL.md"); got != writeNote {
		t.Errorf("the approved Skill's file =\n%s", got)
	}
	if got := pathLines(p.MustRun("simulate", "Note").Stdout); strings.Join(got, "\n") != "1. draft → written → kept" {
		t.Errorf("simulate Note once approved = %q", got)
	}
}

func TestAProposalWhosePlaybookWouldFailCheckIsNotProposed(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: own\n")
	for name, c := range map[string]struct{ proposal, want string }{
		"a Binding to a Skill nobody writes": {
			typeProposal("notes", "Note", noteType, nil),
			"as the Proposal would make it, the Playbook has 1 problem:\n  .jigflow/types/note.yaml: the Binding of \"draft\" names Skill \"write-note\", which the Playbook doesn't declare",
		},
		"a Type whose file would be outside the Playbook": {
			typeProposal("notes", "../../evil", strings.Replace(noteType, "name: Note", "name: ../../evil", 1), map[string]string{"write-note": writeNote}),
			`an Artifact Type's file is named after it, so its name can't start with '.' or hold '/'`,
		},
		"a Type named apart from its text": {
			typeProposal("notes", "Memo", noteType, map[string]string{"write-note": writeNote}),
			`the text of Artifact Type "Memo" declares "Note": its name must be the item's`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			p.Write("author.yaml", c.proposal)
			r := p.RunInSession("A", "propose", "author.yaml")
			if r.ExitCode != 1 || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("propose exited %d, want 1 saying %q:\n%s", r.ExitCode, c.want, r.Stderr)
			}
		})
	}
	if files := tree(t, p); len(files) != 2 {
		t.Errorf("a refused Proposal changed the project: %v", slices.Collect(maps.Keys(files)))
	}
}

// A Status an Artifact is in can't be dropped without a Playbook Migration
// (ADR 0010), when the Proposal is made or when it is approved.
func TestAProposedTypeThatWouldOrphanArtifactsIsNeitherProposedNorApproved(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: own\n")
	p.Write(".jigflow/types/note.yaml", strings.Replace(noteType, "bindings:\n  draft: write-note\n", "", 1))
	p.MustRun("create", "Note", "--title", "Groceries")
	noWritten := strings.NewReplacer("[draft, written, kept]", "[draft, kept]", "{from: draft, to: written}", "{from: draft, to: kept}", "  - {from: written, to: kept, human: true}\n", "").Replace(noteType)
	p.Write("author.yaml", typeProposal("notes skip written", "Note", noWritten, map[string]string{"write-note": writeNote}))
	p.MustRun("propose", "author.yaml")

	p.MustRun("move", "N-1", "written")
	before := tree(t, p)
	if r := p.Run("simulate", "Note", "--proposal", "P-1"); r.ExitCode != 1 || !strings.Contains(r.Stderr, `N-1 "Groceries": Note has no Status "written"`) {
		t.Errorf("simulating a Proposal that orphans N-1 exited %d:\n%s", r.ExitCode, r.Stderr)
	}
	term := p.StartInTerminal("approve", "P-1")
	term.Expect("[y/N] ")
	term.Type("y\n")
	if r := term.Wait(); r.ExitCode != 1 || !strings.Contains(r.Output, `N-1 "Groceries": Note has no Status "written"`) {
		t.Errorf("approving a Proposal that orphans N-1 exited %d:\n%s", r.ExitCode, r.Output)
	}
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("a Proposal not applied changed the project:\n%s", p.Read(".jigflow/types/note.yaml"))
	}

	p.Write("again.yaml", typeProposal("notes skip written", "Note", noWritten, map[string]string{"write-note": writeNote}))
	if r := p.Run("propose", "again.yaml"); r.ExitCode != 1 || !strings.Contains(r.Stderr, `N-1 "Groceries": Note has no Status "written"`) {
		t.Errorf("proposing a Type that orphans N-1 exited %d:\n%s", r.ExitCode, r.Stderr)
	}
}

// Changing an extended Playbook: a proposed Type overrides the Base
// Playbook's of the same name, in a file of the project's, and a later one
// replaces that file.
func TestAProposedTypeOverridesTheBasePlaybooksAndReplacesTheProjectsOwn(t *testing.T) {
	p := larapilot(t)
	story := `name: Story
prefix: S
links:
  implements: Requirement
statuses: [planned, accepted, shipped]
initial: [planned]
final: [shipped]
transitions:
  - {from: planned, to: accepted, human: true}
  - {from: accepted, to: shipped, human: true}
`
	p.Write("story.yaml", typeProposal("ship accepted Stories", "Story", story, nil))
	p.RunInSession("A", "propose", "story.yaml")
	if got := pathLines(p.MustRun("simulate", "Story", "--proposal", "P-1").Stdout); strings.Join(got, "\n") != "1. planned → accepted → shipped" {
		t.Errorf("simulate Story --proposal P-1 = %q", got)
	}
	approveInTerminal(t, p, "P-1")
	if got := p.Read(".jigflow/types/story.yaml"); got != story {
		t.Errorf("the project's Story file =\n%s", got)
	}

	released := strings.NewReplacer("shipped", "released").Replace(story)
	p.Write("story.yaml", typeProposal("release accepted Stories", "Story", released, nil))
	p.RunInSession("A", "propose", "story.yaml")
	approveInTerminal(t, p, "P-2")
	if got := pathLines(p.MustRun("simulate", "Story").Stdout); strings.Join(got, "\n") != "1. planned → accepted → released" {
		t.Errorf("simulate Story = %q", got)
	}
	var types []string
	for file := range tree(t, p) {
		if strings.HasPrefix(file, ".jigflow/types/") {
			types = append(types, file)
		}
	}
	if len(types) != 1 || p.Read(".jigflow/types/story.yaml") != released {
		t.Errorf("the second Proposal should replace the project's Story file, not add one: %v", types)
	}
}

// playbook-author changes a Type by copying its declaration, the Base
// Playbook's too, which simulate prints with --source.
func TestSimulatePrintsTheFileDeclaringTheTypeWithSource(t *testing.T) {
	p := larapilot(t)

	r := p.MustRun("simulate", "Story", "--source")
	if want := "# builtin:larapilot/types/3-story.yaml\n"; !strings.HasPrefix(r.Stdout, want) {
		t.Errorf("simulate --source should start with %q:\n%s", want, r.Stdout)
	}
	for _, want := range []string{"name: Story\nprefix: ST\n", "\nStory: 1 path from an initial Status to a final one\n"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("simulate --source should print %q:\n%s", want, r.Stdout)
		}
	}

	p.Write("note.yaml", typeProposal("notes", "Note", noteType, map[string]string{"write-note": writeNote}))
	p.RunInSession("A", "propose", "note.yaml")
	if r := p.MustRun("simulate", "Note", "--proposal", "P-1", "--source"); !strings.HasPrefix(r.Stdout, "# .jigflow/types/note.yaml\n"+noteType) {
		t.Errorf("simulate --source of a proposed Type should print its proposed file:\n%s", r.Stdout)
	}
}

func TestBothShippedPlaybooksShipPlaybookAuthorForAPersonToStart(t *testing.T) {
	for name, p := range map[string]func(*testing.T) string{
		"Larapilot-style": func(t *testing.T) string {
			p := larapilot(t)
			p.MustRun("publish", "claude-code")
			return p.Read(".claude/skills/jigflow/SKILL.md")
		},
		"Pocock": func(t *testing.T) string {
			p, _ := pocock(t)
			return p.Read(".claude/skills/jigflow/SKILL.md")
		},
	} {
		t.Run(name, func(t *testing.T) {
			if router := p(t); !strings.Contains(router, "\n- /playbook-author: Build this project's Playbook") {
				t.Errorf("the router should name /playbook-author among the Skills a person starts:\n%s", router)
			}
		})
	}
}

// playbook-author asks for the Confirmation of the Proposal it shows the
// person through jfl's tools, and tells authors to end their own Skills the
// same way.
func TestPlaybookAuthorAsksThroughJflsToolsAndTellsAuthorsToEndSkillsSo(t *testing.T) {
	for name, p := range map[string]func(*testing.T) string{
		"Larapilot-style": func(t *testing.T) string {
			p := larapilot(t)
			p.MustRun("publish", "claude-code")
			return p.Read(".claude/skills/playbook-author/SKILL.md")
		},
		"Pocock": func(t *testing.T) string {
			p, _ := pocock(t)
			return p.Read(".claude/skills/playbook-author/SKILL.md")
		},
	} {
		t.Run(name, func(t *testing.T) {
			skill := p(t)
			assertAsksThroughJflsTools(t, "playbook-author", skill, "approve")
			if want := "A Skill that ends in a Proposal or a Human Transition ends by asking the person for their Confirmation"; !strings.Contains(skill, want) {
				t.Errorf("playbook-author should tell authors to end such Skills by asking, containing %q:\n%s", want, skill)
			}
		})
	}
}
