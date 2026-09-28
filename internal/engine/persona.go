package engine

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
)

// PersonaType is the name of the built-in Artifact Type every Playbook has:
// a Persona is an Artifact, which an agent may propose and only a person
// activates (ADR 0004). Its title is the Persona's name, and its body the
// Markdown describing it.
const PersonaType = "Persona"

// The Statuses of a Persona.
const (
	PersonaProposed = "proposed" // put forward, by an agent or a person; not yet usable
	PersonaActive   = "active"   // usable: Adapters publish it
	PersonaRetired  = "retired"  // no longer usable
)

// PersonaArtifactType is the built-in Persona Artifact Type. An agent may
// file a Persona into proposed, its Inbox, since nothing there is agent
// work; every decision after that is a Human Transition. active and
// retired are final: no work waits on a Persona there, although a person
// may still retire an active one or bring a retired one back.
func PersonaArtifactType() *ArtifactType {
	human := func(from, to string) Transition { return Transition{From: from, To: to, Human: true} }
	return &ArtifactType{
		Name:     PersonaType,
		Prefix:   "PERSONA",
		Statuses: []string{PersonaProposed, PersonaActive, PersonaRetired},
		Initial:  []string{PersonaProposed},
		Final:    []string{PersonaActive, PersonaRetired},
		Inbox:    []string{PersonaProposed},
		Transitions: []Transition{
			human(PersonaProposed, PersonaActive),
			human(PersonaProposed, PersonaRetired),
			human(PersonaActive, PersonaRetired),
			human(PersonaRetired, PersonaActive),
		},
	}
}

// personaName is what a Persona's name may be: Skills name it, and
// Adapters publish it as a file of that name.
var personaName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// checkPersonaName refuses a new Persona whose title can't be a Persona's
// name, or names a Persona that is already an Artifact: that one is
// activated or retired instead, so each name is one Persona.
func checkPersonaName(title string, existing []Artifact) error {
	if !personaName.MatchString(title) {
		return fmt.Errorf("a Persona's title is its name, which Skills refer to: letters, digits, '.', '_' and '-', such as security-auditor; not %q", title)
	}
	i := slices.IndexFunc(existing, func(a Artifact) bool { return a.Type == PersonaType && a.Title == title })
	if i >= 0 {
		return fmt.Errorf("Persona %q is already %s, in %s", title, existing[i].ID, existing[i].Status)
	}
	return nil
}

// ActivePersonas is every Persona usable in the project, by name, with the
// Markdown describing it. A Persona of the user's Persona Library is
// usable unless the project has one of the same name, which overrides it:
// a Persona the Playbook ships, usable as it is, or a Persona Artifact a
// person has decided on, active or retired. A Persona Artifact overrides
// the Playbook's too, and one that is only proposed overrides nothing yet.
// bodies holds the body of each Persona Artifact, by id.
func ActivePersonas(library, playbook map[string]string, artifacts []Artifact, bodies map[string]string) map[string]string {
	active := maps.Clone(library)
	if active == nil {
		active = map[string]string{}
	}
	maps.Copy(active, playbook)
	for _, a := range artifacts {
		if a.Type != PersonaType {
			continue
		}
		switch a.Status {
		case PersonaActive:
			active[a.Title] = bodies[a.ID]
		case PersonaRetired:
			delete(active, a.Title)
		}
	}
	return active
}
