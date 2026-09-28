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
	Initial     []string          // Statuses an Artifact may start in
	Final       []string          // Statuses where work on an Artifact ends
	Bindings    map[string]string // Status -> Skill; a Status with no Binding is human work
	Transitions []Transition
}

// Transition is a declared move from one Status to another.
type Transition struct {
	From string
	To   string
}

// Artifact is one unit of workflow state.
type Artifact struct {
	ID     string
	Type   string
	Status string
	Title  string
}

// Create decides a new Artifact of the named Type. An empty status means the
// Type's first initial Status. existing is every Artifact already in the Store,
// used to allocate the next id for the Type's prefix.
func Create(pb *Playbook, typeName, title, status string, existing []Artifact) (Artifact, error) {
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
	return Artifact{ID: nextID(t, existing), Type: t.Name, Status: status, Title: title}, nil
}

// Move decides moving an Artifact to the Status to. It is refused unless the
// Artifact's Type declares a Transition from its current Status to to.
func Move(pb *Playbook, a Artifact, to string) (Artifact, error) {
	t := pb.Type(a.Type)
	if t == nil {
		return Artifact{}, fmt.Errorf("%s has Artifact Type %q, which the Playbook doesn't declare", a.ID, a.Type)
	}
	if !slices.ContainsFunc(t.Transitions, func(tr Transition) bool { return tr.From == a.Status && tr.To == to }) {
		msg := fmt.Sprintf("%s: %q → %q is not a declared Transition.", a.ID, a.Status, to)
		if out := t.outgoing(a.Status); len(out) > 0 {
			msg += fmt.Sprintf(" From %q it can move to: %s.", a.Status, strings.Join(out, ", "))
		} else {
			msg += fmt.Sprintf(" No Transition is declared from %q.", a.Status)
		}
		return Artifact{}, errors.New(msg)
	}
	a.Status = to
	return a, nil
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

func typeNames(pb *Playbook) []string {
	names := make([]string, len(pb.Types))
	for i, t := range pb.Types {
		names[i] = t.Name
	}
	return names
}
