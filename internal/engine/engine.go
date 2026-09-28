// Package engine is JigFlow's workflow engine: a pure core with no I/O.
//
// It decides whether an Artifact may be created or moved, and which Skill
// `next` hands to an agent. The shell around it (package cli) loads the
// Playbook, reads and writes the Store, and reports the results. Every
// decision the engine refuses comes back as an error whose message is meant
// for the person or agent at the terminal.
package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Playbook is the complete declared way of working for a project.
type Playbook struct {
	Name  string
	Types []*ArtifactType // in declaration order
}

// Type returns the Artifact Type with the given name, or nil.
func (p *Playbook) Type(name string) *ArtifactType {
	for _, t := range p.Types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// ArtifactType is a user-declared kind of Artifact with its own Statuses.
type ArtifactType struct {
	Name        string
	Prefix      string // Artifact ids are "<Prefix>-<n>"
	Statuses    []string
	Initial     []string               // Statuses an Artifact may start in
	Final       []string               // Statuses where work on an Artifact ends
	Bindings    map[string]string      // Status -> Skill; a Status with no Binding is human work
	Links       map[string]string      // Link name -> the Artifact Type it points to
	Readiness   map[string][]Condition // Status -> what must hold before agent work there may start
	Transitions []Transition
}

// Condition is a declared condition on the Statuses of Linked Artifacts: the
// Readiness of a Status, or a Guard on a Transition.
type Condition struct {
	Kind     string   // "linked-all-in" or "has-incoming"
	Link     string   // the Link it looks at
	Statuses []string // linked-all-in: every Artifact the Link points to is in one of these
	Min      int      // has-incoming: at least this many Artifacts link here through Link
}

// Transition is a declared move from one Status to another.
type Transition struct {
	From    string
	To      string
	Guards  []Condition // must all hold at the moment of the move
	Gates   []Command   // must all succeed, in order, for the Transition to happen
	Actions []Command   // run in order after the Transition succeeds
}

// Command is a user-declared shell command: a Gate or an Action.
type Command struct {
	Name string
	Cmd  string
}

// Artifact is one unit of workflow state.
type Artifact struct {
	ID     string
	Type   string
	Status string
	Title  string
	Links  map[string][]string // Link name -> ids of the linked Artifacts
}

// Create decides a new Artifact of the named Type. An empty status means the
// Type's first initial Status. existing is every Artifact already in the Store,
// used to allocate the next id for the Type's prefix.
//
// links maps each Link name to the ids of the Artifacts it points to.
func Create(pb *Playbook, typeName, title, status string, links map[string][]string, existing []Artifact) (Artifact, error) {
	t := pb.Type(typeName)
	if t == nil {
		return Artifact{}, fmt.Errorf("unknown Artifact Type %q. Declared Types: %s", typeName, strings.Join(typeNames(pb), ", "))
	}
	if strings.TrimSpace(title) == "" {
		return Artifact{}, fmt.Errorf("a %s needs a title", t.Name)
	}
	if status == "" && len(t.Initial) > 0 {
		status = t.Initial[0]
	}
	if !slices.Contains(t.Initial, status) {
		return Artifact{}, fmt.Errorf("a %s can't start in %q. Allowed starting Statuses: %s", t.Name, status, strings.Join(t.Initial, ", "))
	}
	for _, name := range slices.Sorted(maps.Keys(links)) {
		target, ok := t.Links[name]
		if !ok {
			return Artifact{}, fmt.Errorf("a %s has no Link %q. Declared Links: %s", t.Name, name, declared(slices.Sorted(maps.Keys(t.Links))))
		}
		for _, id := range links[name] {
			i := slices.IndexFunc(existing, func(o Artifact) bool { return o.ID == id })
			if i < 0 {
				return Artifact{}, fmt.Errorf("Link %q points to %s, which doesn't exist", name, id)
			}
			if existing[i].Type != target {
				return Artifact{}, fmt.Errorf("Link %q points to a %s, but %s is a %s", name, target, id, existing[i].Type)
			}
		}
	}
	return Artifact{ID: nextID(t, existing), Type: t.Name, Status: status, Title: title, Links: links}, nil
}

// Move decides moving an Artifact to the Status to. It is refused unless the
// Artifact's Type declares a Transition from its current Status to to, the
// Readiness of its current Status holds, and every Guard on the Transition
// holds. all is every Artifact in the Store, so Links can be followed.
//
// Every move is treated as an agent's: Readiness says when agent work in a
// Status may start, so an agent can't move out of a Status that isn't ready.
//
// It returns the moved Artifact and the Transition it takes. The engine
// performs no I/O: the caller runs the Transition's Gates, saves the moved
// Artifact only if they all succeed, and then runs its Actions.
func Move(pb *Playbook, a Artifact, to string, all []Artifact) (Artifact, Transition, error) {
	t := pb.Type(a.Type)
	if t == nil {
		return Artifact{}, Transition{}, fmt.Errorf("%s has Artifact Type %q, which the Playbook doesn't declare", a.ID, a.Type)
	}
	i := slices.IndexFunc(t.Transitions, func(tr Transition) bool { return tr.From == a.Status && tr.To == to })
	if i < 0 {
		msg := fmt.Sprintf("%s: %q → %q is not a declared Transition.", a.ID, a.Status, to)
		if out := t.outgoing(a.Status); len(out) > 0 {
			msg += fmt.Sprintf(" From %q it can move to: %s.", a.Status, strings.Join(out, ", "))
		} else {
			msg += fmt.Sprintf(" No Transition is declared from %q.", a.Status)
		}
		return Artifact{}, Transition{}, errors.New(msg)
	}
	if f := failed(t.Readiness[a.Status], a, all); len(f) > 0 {
		return Artifact{}, Transition{}, fmt.Errorf("%s: not ready. Readiness of %q needs %s", a.ID, a.Status, describe(f))
	}
	tr := t.Transitions[i]
	if f := failed(tr.Guards, a, all); len(f) > 0 {
		return Artifact{}, Transition{}, fmt.Errorf("%s: %q → %q refused: a Guard needs %s", a.ID, a.Status, to, describe(f))
	}
	a.Status = to
	return a, tr, nil
}

// outgoing returns the Statuses reachable from status in one Transition.
func (t *ArtifactType) outgoing(status string) []string {
	var to []string
	for _, tr := range t.Transitions {
		if tr.From == status {
			to = append(to, tr.To)
		}
	}
	return to
}

func nextID(t *ArtifactType, existing []Artifact) string {
	highest := 0
	for _, a := range existing {
		if a.Type != t.Name {
			continue
		}
		if n, ok := idNumber(t.Prefix, a.ID); ok && n > highest {
			highest = n
		}
	}
	return fmt.Sprintf("%s-%d", t.Prefix, highest+1)
}

// idNumber returns n for an id of the form "<prefix>-<n>".
func idNumber(prefix, id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, prefix+"-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// declared lists names for a message, or says there are none.
func declared(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func typeNames(pb *Playbook) []string {
	names := make([]string, len(pb.Types))
	for i, t := range pb.Types {
		names[i] = t.Name
	}
	return names
}
