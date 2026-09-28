package playbook

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// Invalid is the error of a Playbook that fails its checks: every problem
// found, each naming the file to fix.
type Invalid struct {
	Problems []string
}

func (e *Invalid) Error() string {
	n := "1 problem"
	if len(e.Problems) != 1 {
		n = fmt.Sprintf("%d problems", len(e.Problems))
	}
	return fmt.Sprintf("the Playbook has %s:\n  %s", n, strings.Join(e.Problems, "\n  "))
}

// check validates a loaded Playbook as a whole, the way `jfl check` reports
// it: Personas that Skills name but that neither exist nor have a fallback
// description, and Guidelines that Skills name but that don't exist; and,
// per Artifact Type, Statuses no initial Status reaches, Statuses with no
// way out that aren't final, Inboxes from which a Skill that changes code or
// Artifacts is reachable without a Human Transition, undeclared Links, and
// Bindings to missing Skills. typeFiles and skillFiles
// map each Artifact Type and Skill to the file declaring it, for messages.
func check(pb *engine.Playbook, typeFiles, skillFiles map[string]string) []string {
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(pb.Skills)) {
		for _, ref := range pb.Skills[name].Personas {
			if !slices.Contains(pb.Personas, ref.Name) && strings.TrimSpace(ref.Fallback) == "" {
				problems = append(problems, fmt.Sprintf("%s: Persona %q doesn't exist and has no fallback description", skillFiles[name], ref.Name))
			}
		}
		for _, g := range pb.Skills[name].Guidelines {
			if _, ok := pb.Guidelines[g]; !ok {
				problems = append(problems, fmt.Sprintf("%s: Guideline %q doesn't exist", skillFiles[name], g))
			}
		}
	}
	for _, t := range pb.Types {
		report := func(format string, args ...any) {
			problems = append(problems, typeFiles[t.Name]+": "+fmt.Sprintf(format, args...))
		}
		reachable := reach(t, t.Initial, func(engine.Transition) bool { return true })
		for _, status := range t.Statuses {
			if !reachable[status] {
				report("Status %q can't be reached from an initial Status", status)
			}
			wayOut := slices.ContainsFunc(t.Transitions, func(tr engine.Transition) bool { return tr.From == status })
			if !wayOut && !slices.Contains(t.Final, status) {
				report("Status %q has no Transition out and isn't final", status)
			}
		}
		// An agent may create into an Inbox without a person's approval, so
		// no Skill that changes code or Artifacts may be reachable from it
		// but through a Human Transition.
		for _, inbox := range t.Inbox {
			byAgents := reach(t, []string{inbox}, func(tr engine.Transition) bool { return !tr.Human })
			for _, status := range t.Statuses {
				if skill := pb.Skills[t.Bindings[status]]; byAgents[status] && skill != nil && skill.Changes {
					report("%s Inbox %q reaches /%s in %q without a Human Transition", t.Name, inbox, skill.Name, status)
				}
			}
		}
		checkLinks(pb, t, report)
		for _, status := range slices.Sorted(maps.Keys(t.Bindings)) {
			if skill := t.Bindings[status]; pb.Skills[skill] == nil {
				report("the Binding of %q names Skill %q, which the Playbook doesn't declare", status, skill)
			}
		}
	}
	return problems
}

// checkLinks reports Links to undeclared Artifact Types, and Readiness or
// Guards that refer to a Link nobody declares: such a condition could never
// be met as its author meant.
func checkLinks(pb *engine.Playbook, t *engine.ArtifactType, report func(string, ...any)) {
	for _, name := range slices.Sorted(maps.Keys(t.Links)) {
		if pb.Type(t.Links[name]) == nil {
			report("Link %q points to Artifact Type %q, which the Playbook doesn't declare", name, t.Links[name])
		}
	}
	for _, status := range slices.Sorted(maps.Keys(t.Readiness)) {
		for _, problem := range checkConditions(pb, t, t.Readiness[status]) {
			report("Readiness of %q %s", status, problem)
		}
	}
	for _, tr := range t.Transitions {
		for _, problem := range checkConditions(pb, t, tr.Guards) {
			report("a Guard on %q → %q %s", tr.From, tr.To, problem)
		}
	}
}

func checkConditions(pb *engine.Playbook, t *engine.ArtifactType, conds []engine.Condition) []string {
	var problems []string
	for _, c := range conds {
		switch c.Kind {
		case engine.LinkedAllIn:
			if _, ok := t.Links[c.Link]; !ok {
				problems = append(problems, fmt.Sprintf("refers to Link %q, which a %s doesn't declare", c.Link, t.Name))
			}
		case engine.HasIncoming:
			if !slices.ContainsFunc(pb.Types, func(o *engine.ArtifactType) bool { return o.Links[c.Link] == t.Name }) {
				problems = append(problems, fmt.Sprintf("refers to incoming Link %q, which no Artifact Type declares towards %s", c.Link, t.Name))
			}
		default:
			problems = append(problems, fmt.Sprintf("has unknown kind %q (want %s or %s)", c.Kind, engine.LinkedAllIn, engine.HasIncoming))
		}
	}
	return problems
}

// reach returns the Statuses of t reachable from the given ones, themselves
// included, through the Transitions follow accepts.
func reach(t *engine.ArtifactType, from []string, follow func(engine.Transition) bool) map[string]bool {
	seen := map[string]bool{}
	queue := slices.Clone(from)
	for _, s := range from {
		seen[s] = true
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, tr := range t.Transitions {
			if tr.From == s && follow(tr) && !seen[tr.To] {
				seen[tr.To] = true
				queue = append(queue, tr.To)
			}
		}
	}
	return seen
}
