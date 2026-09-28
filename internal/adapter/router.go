package adapter

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// Router is the name of the router Skill every Adapter generates from the
// Playbook's Bindings; no Skill of the Playbook may take it.
const Router = "jigflow"

// routerDescription is what the agent reads to choose the router Skill.
const routerDescription = "Find the next piece of work in this project's JigFlow Playbook and the Skill to run on it. Use when unsure which Skill to run, to pick up the next Artifact, or when a person asks which Skill fits what they want to do."

// routerPrompt is the router Skill's prompt: how to follow the Bindings,
// then, per Artifact Type, what works on each Status, and the Skills only
// a person starts.
func routerPrompt(pb *engine.Playbook) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Route work through the Playbook %q.

1. Run `+"`jfl next`"+`. It names the Skill to run and the Artifact to run it on, and makes that Artifact your Focus.
2. Run that Skill on that Artifact, the way its Binding below says. Move the Artifact on only with `+"`jfl move`"+`, never by editing its frontmatter.
3. When `+"`jfl next`"+` says there is nothing for an agent to do, stop: what is left is a person's.

`+autopilot+`

What works on each Status of each Artifact Type:
`, pb.Name)
	b.WriteString(statuses(pb, "##"))
	b.WriteString(personStarted(pb))
	return b.String()
}

// personStarted names the Skills only a person starts, with their
// descriptions, so the agent can point a person asking which Skill fits
// their situation to one: such a Skill's description is out of the agent's
// reach. It is empty when the Playbook has none.
func personStarted(pb *engine.Playbook) string {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(pb.Skills)) {
		if s := pb.Skills[name]; s.Invocation == engine.InvokedByUser {
			if s.Description == "" {
				fmt.Fprintf(&b, "- /%s\n", s.Name)
			} else {
				fmt.Fprintf(&b, "- /%s: %s\n", s.Name, s.Description)
			}
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n## Skills only a person starts\n\nWhen a person asks which Skill fits what they want to do, point them to one of these, which only they can start, by typing its name:\n\n" + b.String()
}

// autopilot tells an agent how to run autopilot, which it drives: jfl runs
// no agent loop (ADR 0001).
const autopilot = "When a person asks you to work on your own, run autopilot: run `jfl next --autopilot` instead of `jfl next`, run the Skill it names on that Artifact, and repeat. Stop as soon as it says `autopilot stopped`, and tell the person why it stopped and what is waiting for a person, as it lists them. A refused `jfl move` you can't fix within the Skill, such as a Human Transition, ends the run at the next step."

// statuses lists, under a heading of the given level per Artifact Type,
// what works on each of its Statuses.
func statuses(pb *engine.Playbook, level string) string {
	var b strings.Builder
	for _, t := range pb.Types {
		fmt.Fprintf(&b, "\n%s %s\n\n", level, t.Name)
		for _, status := range t.Statuses {
			fmt.Fprintf(&b, "- %s: %s\n", status, atStatus(t, status))
		}
	}
	return b.String()
}

// atStatus says what works on an Artifact of t in status: its Binding's
// Skill, run the way the Binding asks, or a person.
func atStatus(t *engine.ArtifactType, status string) string {
	if slices.Contains(t.Final, status) {
		return "final"
	}
	skill := t.Bindings[status]
	if skill == "" {
		return "human work, never an agent's"
	}
	var how []string
	if h := t.Hints[status]; h.Fresh {
		how = append(how, "a fresh session")
	}
	if h := t.Hints[status]; h.Isolated {
		how = append(how, "an isolated sub-agent")
	}
	if len(how) == 0 {
		return "/" + skill
	}
	return "/" + skill + ", in " + strings.Join(how, " and ")
}
