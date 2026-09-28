package adapter

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// Router is the name of the router Skill every Adapter generates from the
// Playbook's Bindings; no Skill of the Playbook may take it.
const Router = "jigflow"

// routerDescription is what the agent reads to choose the router Skill.
const routerDescription = "Find the next piece of work in this project's JigFlow Playbook and the Skill to run on it. Use when unsure which Skill to run, or to pick up the next Artifact."

// routerPrompt is the router Skill's prompt: how to follow the Bindings,
// then, per Artifact Type, what works on each Status.
func routerPrompt(pb *engine.Playbook) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Route work through the Playbook %q.

1. Run `+"`jfl next`"+`. It names the Skill to run and the Artifact to run it on, and makes that Artifact your Focus.
2. Run that Skill on that Artifact, the way its Binding below says. Move the Artifact on only with `+"`jfl move`"+`, never by editing its frontmatter.
3. When `+"`jfl next`"+` says there is nothing for an agent to do, stop: what is left is a person's.

What works on each Status of each Artifact Type:
`, pb.Name)
	b.WriteString(statuses(pb, "##"))
	return b.String()
}

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
