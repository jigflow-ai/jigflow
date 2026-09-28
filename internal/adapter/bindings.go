package adapter

import (
	"fmt"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// binding is one Binding of a Skill: the Artifact Type and Status whose
// Artifacts it works on.
type binding struct {
	Type   *engine.ArtifactType
	Status string
	Hints  engine.Hints
}

// bindingsOf returns the Bindings of the named Skill, in the declaration
// order of the Artifact Types and their Statuses.
func bindingsOf(pb *engine.Playbook, skill string) []binding {
	var bs []binding
	for _, t := range pb.Types {
		for _, status := range t.Statuses {
			if t.Bindings[status] == skill {
				bs = append(bs, binding{Type: t, Status: status, Hints: t.Hints[status]})
			}
		}
	}
	return bs
}

// description is what the agent reads to choose Skill s. A bound Skill's
// says it runs on what jfl next names, and where it is bound.
func description(s *engine.Skill, bs []binding) string {
	if s.Invocation != engine.InvokedByBinding || len(bs) == 0 {
		return s.Description
	}
	d := "Run it when jfl next names it, on the Artifact it names: " + where(bs) + "."
	if s.Description == "" {
		return d
	}
	return s.Description + " " + d
}

// where names the Statuses of each Artifact Type the Bindings bs are in,
// e.g. "a Ticket in ready-for-agent or in-progress; an Issue in triage".
func where(bs []binding) string {
	var parts []string
	for i := 0; i < len(bs); {
		t := bs[i].Type
		var statuses []string
		for ; i < len(bs) && bs[i].Type == t; i++ {
			statuses = append(statuses, bs[i].Status)
		}
		parts = append(parts, fmt.Sprintf("%s %s in %s", article(t.Name), t.Name, strings.Join(statuses, " or ")))
	}
	return strings.Join(parts, "; ")
}

// article is the indefinite article before name.
func article(name string) string {
	if strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an"
	}
	return "a"
}

// isolated reports whether every one of the Bindings bs asks for its Skill
// to run in an isolated sub-agent.
func isolated(bs []binding) bool {
	if len(bs) == 0 {
		return false
	}
	for _, b := range bs {
		if !b.Hints.Isolated {
			return false
		}
	}
	return true
}

// advice tells the agent the Hints of the Bindings bs that its coding agent
// can't apply itself: every fresh session, and every isolated sub-agent
// unless the Skill already runs in one. It is empty when there are none.
func advice(bs []binding, forked bool) string {
	var fresh, sub []binding
	for _, b := range bs {
		if b.Hints.Fresh {
			fresh = append(fresh, b)
		}
		if b.Hints.Isolated && !forked {
			sub = append(sub, b)
		}
	}
	var b strings.Builder
	if len(fresh) > 0 {
		b.WriteString("> On " + where(fresh) + ", run this Skill in a fresh session: start a new session (or /clear) before you run it.\n")
	}
	if len(sub) > 0 {
		b.WriteString("> On " + where(sub) + ", run this Skill in an isolated sub-agent.\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String()
}
