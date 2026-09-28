package adapter

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// An Adapter publishes only active Personas (ADR 0004): personas maps the
// name of each Persona usable in the project to the Markdown describing
// it, the Persona Library's, the Playbook's and the active Persona
// Artifacts, as the CLI resolved them.

// personaPath is where a Persona is published, relative to the SKILL.md of
// a Skill that names it or to the directory of every active Persona.
func personaPath(name string) string { return "personas/" + name + ".md" }

// skillPersonaFiles publishes the active Personas Skill s names next to its
// SKILL.md, in dir, as Guidelines are, so the Skill carries what it needs.
func skillPersonaFiles(s *engine.Skill, personas map[string]string, dir string) []File {
	var files []File
	for _, ref := range s.Personas {
		if md, ok := personas[ref.Name]; ok {
			files = append(files, File{Path: dir + "/" + personaPath(ref.Name), Content: md})
		}
	}
	return files
}

// personasSection closes a published SKILL.md with the Personas s names:
// a link to each active one, to adopt, and for each that isn't active, how
// to propose it from the Skill's fallback description.
func personasSection(s *engine.Skill, personas map[string]string) string {
	if len(s.Personas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Personas\n\n")
	for _, ref := range s.Personas {
		if _, ok := personas[ref.Name]; ok {
			fmt.Fprintf(&b, "- Adopt the point of view of the %s Persona: read [%s](%s).\n", ref.Name, ref.Name, personaPath(ref.Name))
			continue
		}
		fmt.Fprintf(&b, "- The %s Persona isn't active in this project, so work without it.", ref.Name)
		if fallback := strings.TrimSpace(ref.Fallback); fallback != "" {
			fmt.Fprintf(&b, " If the work needs it, propose it: run `jfl create Persona --title %s`, write this description into the body of the file it creates in .jigflow/state, and tell the person, who may activate it:\n\n  > %s\n", ref.Name, strings.ReplaceAll(fallback, "\n", "\n  > "))
		} else {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// personaFiles publishes every active Persona under dir, in personas/, for
// the agent to adopt when a person or the work asks for it, although no
// Skill names it.
func personaFiles(personas map[string]string, dir string) []File {
	var files []File
	for _, name := range slices.Sorted(maps.Keys(personas)) {
		files = append(files, File{Path: dir + "/" + personaPath(name), Content: personas[name]})
	}
	return files
}

// personasGuide tells the agent what Personas are, lists the active ones,
// published under dir (relative to where the guide is read), and says how
// to propose a new one when the conversation shows it is needed.
func personasGuide(personas map[string]string, dir string) string {
	var b strings.Builder
	b.WriteString("A Persona is a point of view a Skill, or a person, may ask you to adopt; it is not a separate agent and has no memory of its own.")
	if len(personas) == 0 {
		b.WriteString(" No Persona is active in this project yet.\n")
	} else {
		b.WriteString(" Active in this project:\n\n")
		for _, name := range slices.Sorted(maps.Keys(personas)) {
			fmt.Fprintf(&b, "- [%s](%s)\n", name, dir+personaPath(name))
		}
	}
	b.WriteString("\nWhen the conversation shows that a point of view no active Persona gives is needed, propose a Persona: run `jfl create Persona --title <name>`, describe it in the body of the file it creates in .jigflow/state, and tell the person. Adopt it only once a person has activated it and it has been published.\n")
	return b.String()
}
