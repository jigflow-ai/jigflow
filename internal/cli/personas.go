package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// activePersonas is every Persona usable in the project, by name, with the
// Markdown describing it: those of the user's Persona Library, of the
// Playbook, and the Persona Artifacts, the project's overriding the
// Library's (see engine.ActivePersonas). Persona Artifacts are always
// files, the built-in Type naming no Connector.
func (e *env) activePersonas(pb *engine.Playbook) (map[string]string, error) {
	library, err := playbook.Library(e.getenv)
	if err != nil {
		return nil, err
	}
	files := store.NewFile(e.dir)
	all, err := files.List()
	if err != nil {
		return nil, err
	}
	var personas []engine.Artifact
	bodies := map[string]string{}
	for _, a := range all {
		if a.Type != engine.PersonaType {
			continue
		}
		personas = append(personas, a)
		body, err := files.Body(a.ID)
		if err != nil {
			return nil, err
		}
		// The body starts after the frontmatter's closing line; a Persona
		// is published as the Markdown describing it.
		bodies[a.ID] = strings.TrimLeft(body, "\n")
	}
	return engine.ActivePersonas(library, pb.Personas, personas, bodies), nil
}

// usablePersona refuses a comment's Persona name, when it has one, unless
// it names a Persona usable in the project, resolved as jfl publish
// resolves them: jfl vouches for the name, not for the point of view
// (ADR 0028).
func (e *env) usablePersona(pb *engine.Playbook, name string) error {
	if name == "" {
		return nil
	}
	usable, err := e.activePersonas(pb)
	if err != nil {
		return err
	}
	if _, ok := usable[name]; ok {
		return nil
	}
	if len(usable) == 0 {
		return fmt.Errorf("no Persona %q is usable in this project, nor any other; comment without --persona, or propose it with jfl create Persona --title %s, for a person to activate: nothing was added", name, name)
	}
	return fmt.Errorf("no Persona %q is usable in this project; the usable ones are %s: nothing was added", name, strings.Join(slices.Sorted(maps.Keys(usable)), ", "))
}
