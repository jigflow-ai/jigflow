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
	Name       string
	Types      []*ArtifactType       // in declaration order
	Skills     map[string]*Skill     // by name
	Personas   map[string]string     // the Personas the Playbook ships: name -> its Markdown
	Guidelines map[string]string     // the Guidelines the project declares: name -> its Markdown
	Connectors map[string]*Connector // the project's Connectors, by name
	Gates      map[string]string     // the command the Playbook file gives each Gate, by the Gate's name
}

// Connector is how the project reaches an outside tracker that keeps the
// Artifacts of some Artifact Types (ADR 0008): the executable to run, and
// the project's settings for it.
type Connector struct {
	Name     string
	Command  string   // the executable; a relative path is the project root's
	Args     []string // arguments it is run with
	Marker   string   // the AI-generated marker added to text agents write into the tracker
	Settings map[string]any
	Types    map[string]TrackerMapping // Artifact Type -> how its Artifacts look in the tracker
}

// TrackerMapping is how the Artifacts of one Artifact Type look in a
// tracker: the project settings that map its Statuses and field values to
// the tracker's labels or states. A Status or value it doesn't map is a
// label of the same name.
type TrackerMapping struct {
	Settings map[string]any                    // merged over the Connector's for this Type
	Statuses map[string]TrackerTerm            // Status -> its label or state
	Fields   map[string]map[string]TrackerTerm // field -> value -> its label or state
}

// TrackerTerm is a label, a state, or both, in a tracker.
type TrackerTerm struct {
	Label string
	State string
}

// Skill is a prompt file that tells an agent how to do one piece of work.
type Skill struct {
	Name        string
	Description string // what it does and when to use it, for the agent choosing it
	Changes     bool   // whether it changes code or Artifacts
	Invocation  string // its Invocation Mode: InvokedByUser, InvokedByAgent or InvokedByBinding
	Personas    []PersonaRef
	Guidelines  []string // names of the Guidelines it may load
	Prompt      string   // the Markdown of its SKILL.md, after the frontmatter
}

// PersonaRef is a Skill naming a Persona for the agent to adopt, with a
// fallback description from which a missing Persona can be proposed.
type PersonaRef struct {
	Name     string
	Fallback string
}

// Invocation Modes: how a Skill may be started.
const (
	InvokedByUser    = "user"  // by a person only
	InvokedByAgent   = "agent" // by the agent whenever relevant
	InvokedByBinding = "bound" // through a Binding
)

// Type returns the Artifact Type with the given name, or nil.
func (p *Playbook) Type(name string) *ArtifactType {
	for _, t := range p.Types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// ArtifactType is a user-declared kind of Artifact with its own fields,
// Statuses and Store.
type ArtifactType struct {
	Name        string
	Prefix      string              // Artifact ids are "<Prefix>-<n>"
	Store       string              // the Connector keeping its Artifacts; empty for the file Store
	Fields      map[string][]string // field -> the values it may take
	Statuses    []string
	Initial     []string               // Statuses an Artifact may start in
	Final       []string               // Statuses where work on an Artifact ends
	Inbox       []string               // Statuses agents may create into although they have a Binding
	Bindings    map[string]string      // Status -> Skill; a Status with no Binding is human work
	Hints       map[string]Hints       // Status -> how its Binding asks for the Skill to run
	Links       map[string]string      // Link name -> the Artifact Type it points to
	Readiness   map[string][]Condition // Status -> what must hold before agent work there may start
	Transitions []Transition
	Migrations  map[string]string // an old, undeclared Status -> the Status its Artifacts migrate to
	File        string            // the file declaring it, as its author knows it; empty when built in
	Source      string            // that file, as written
}

// Hints are how a Binding asks for its Skill to run, which Adapters
// translate where the coding agent supports it.
type Hints struct {
	Fresh    bool // in a fresh session, with a clean context
	Isolated bool // in an isolated sub-agent
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
	From      string
	To        string
	Human     bool        // a Human Transition: only a person may make it
	Dashboard bool        // a Human Transition made only in the Dashboard, never in a terminal (ADR 0017)
	Guards    []Condition // must all hold at the moment of the move
	Gates     []Command   // must all succeed, in order, for the Transition to happen
	Actions   []Command   // run in order after the Transition succeeds
}

// Actor is who asks the engine for a move: an agent session, identified by
// the session id its Adapter gives it, or a person, who has none.
type Actor struct {
	Session string
}

// Agent reports whether the Actor is an agent session.
func (ac Actor) Agent() bool { return ac.Session != "" }

// Command is a user-declared shell command: a Gate or an Action.
type Command struct {
	Name string
	Cmd  string // empty for a Gate the Playbook leaves the project to give a command
}

// Artifact is one unit of workflow state.
type Artifact struct {
	ID     string
	Type   string
	Status string
	Title  string
	Fields map[string]string   // field -> its value
	Links  map[string][]string // Link name -> ids of the linked Artifacts
	Claim  string              // the agent session that owns the work on it; empty for none
}

// Create decides a new Artifact of the named Type on behalf of actor. An
// empty status means the Type's first initial Status. existing is every
// Artifact already in the Store, used to allocate the next id for the Type's
// prefix.
//
// fields maps each of the Type's fields to one of the values it declares,
// and links maps each Link name to the ids of the Artifacts it points to.
//
// Creating into a Status that has a Binding hands the Artifact to an agent
// immediately, so it counts as a Human Transition: an agent may do it only
// into an Inbox, and must otherwise put the creation in a Proposal.
func Create(pb *Playbook, actor Actor, typeName, title, status string, fields map[string]string, links map[string][]string, existing []Artifact) (Artifact, error) {
	t := pb.Type(typeName)
	if t == nil {
		return Artifact{}, fmt.Errorf("unknown Artifact Type %q. Declared Types: %s", typeName, strings.Join(typeNames(pb), ", "))
	}
	if strings.TrimSpace(title) == "" {
		return Artifact{}, fmt.Errorf("a %s needs a title", t.Name)
	}
	if t.Name == PersonaType {
		if err := checkPersonaName(title, existing); err != nil {
			return Artifact{}, err
		}
	}
	if status == "" && len(t.Initial) > 0 {
		status = t.Initial[0]
	}
	if !slices.Contains(t.Initial, status) {
		return Artifact{}, fmt.Errorf("a %s can't start in %q. Allowed starting Statuses: %s", t.Name, status, strings.Join(t.Initial, ", "))
	}
	if actor.Agent() && t.Bindings[status] != "" && !slices.Contains(t.Inbox, status) {
		return Artifact{}, fmt.Errorf("Creating a %s straight into %q would hand it to an agent immediately. An agent must put it in a Proposal for a human to approve.", t.Name, status)
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		values, ok := t.Fields[name]
		if !ok {
			return Artifact{}, fmt.Errorf("a %s has no field %q. Declared fields: %s", t.Name, name, declared(slices.Sorted(maps.Keys(t.Fields))))
		}
		if !slices.Contains(values, fields[name]) {
			return Artifact{}, fmt.Errorf("a %s's %s can't be %q. Declared values: %s", t.Name, name, fields[name], strings.Join(values, ", "))
		}
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
	if len(fields) == 0 {
		fields = nil
	}
	return Artifact{ID: nextID(t, existing), Type: t.Name, Status: status, Title: title, Fields: fields, Links: links}, nil
}

// Move decides moving an Artifact to the Status to on behalf of actor. It is
// refused unless the Artifact's Type declares a Transition from its current
// Status to to, an agent isn't attempting a Human Transition or moving an
// Artifact another session has claimed, the Readiness of its current Status
// holds (for an agent), and every Guard on the Transition holds. all is every
// Artifact in the Store, so Links can be followed.
//
// An agent's Transition Claims the Artifact for its session; a person isn't
// held to Claims and doesn't take one. Entering a Status with no Binding, or
// a final Status, releases the Claim, so work handed back to a person, or
// finished, can be picked up by any session.
//
// Readiness says when agent work in a Status may start, so an agent can't
// move out of a Status that isn't ready; a person isn't held to it.
//
// A Human Transition by a person still needs that person's confirmation;
// asking for it is the caller's job, since the engine performs no I/O.
//
// It returns the moved Artifact and the Transition it takes. The engine
// performs no I/O: the caller runs the Transition's Gates, saves the moved
// Artifact only if they all succeed, and then runs its Actions.
func Move(pb *Playbook, actor Actor, a Artifact, to string, all []Artifact) (Artifact, Transition, error) {
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
	tr := t.Transitions[i]
	if tr.Human && actor.Agent() {
		return Artifact{}, Transition{}, fmt.Errorf("%s: %q → %q is a Human Transition. An agent can only propose it.", a.ID, a.Status, to)
	}
	if actor.Agent() && a.Claim != "" && a.Claim != actor.Session {
		return Artifact{}, Transition{}, fmt.Errorf("%s is claimed by agent session %s.", a.ID, a.Claim)
	}
	if f := failed(t.Readiness[a.Status], a, all); len(f) > 0 && actor.Agent() {
		return Artifact{}, Transition{}, fmt.Errorf("%s: not ready. Readiness of %q needs %s", a.ID, a.Status, describe(f))
	}
	if f := failed(tr.Guards, a, all); len(f) > 0 {
		return Artifact{}, Transition{}, fmt.Errorf("%s: %q → %q refused: a Guard needs %s", a.ID, a.Status, to, describe(f))
	}
	a.Status = to
	if actor.Agent() {
		a.Claim = actor.Session
	}
	if !t.agentWork(to) {
		a.Claim = ""
	}
	return a, tr, nil
}

// AgentWork reports whether a is work an agent may hold: its Status has a
// Binding and isn't final. Otherwise it is in no session's Claim or Focus.
func (p *Playbook) AgentWork(a Artifact) bool {
	t := p.Type(a.Type)
	return t != nil && t.agentWork(a.Status)
}

// Work is who an Artifact waits on.
type Work string

// Who an Artifact waits on.
const (
	ForAgent Work = "agent work" // its Status has a Binding: an agent's next may hand it out
	ForHuman Work = "human work" // its Status has no Binding: only a person moves it on
	Finished Work = "finished"   // it is in a final Status
)

// WorkOf says who a waits on: an agent, a person, or nobody, being finished.
func (p *Playbook) WorkOf(a Artifact) Work {
	t := p.Type(a.Type)
	switch {
	case t != nil && slices.Contains(t.Final, a.Status):
		return Finished
	case t != nil && t.agentWork(a.Status):
		return ForAgent
	}
	return ForHuman
}

// agentWork reports whether an Artifact in status is work an agent may hold.
func (t *ArtifactType) agentWork(status string) bool {
	return t.Bindings[status] != "" && !slices.Contains(t.Final, status)
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
