// Package playbook loads a project's Playbook from disk: a small Playbook
// file plus one YAML file per Artifact Type.
//
//	.jigflow/playbook.yaml       the Playbook file
//	.jigflow/types/*.yaml        one file per Artifact Type
//	.jigflow/skills/*/SKILL.md   one directory per Skill, named after it
//	.jigflow/personas/*.md       one file per Persona, named after it
//	.jigflow/guidelines/*.md     one file per Guideline, named after it
//
// The Playbook file may extend a single Base Playbook, laid out the same way
// in a directory of its own, whose parts the Playbook overrides by name.
//
// A Playbook that fails any check doesn't load: Load reports every problem
// at once, so `jfl check` and every other command print the same list.
package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// Dir is the project-relative directory holding the Playbook.
const Dir = ".jigflow"

type playbookFile struct {
	Name    string    `yaml:"name"`
	Extends yaml.Node `yaml:"extends"` // the Base Playbook; see parseBaseRef
}

type typeFile struct {
	Name        string                     `yaml:"name"`
	Prefix      string                     `yaml:"prefix"`
	Statuses    []string                   `yaml:"statuses"`
	Initial     []string                   `yaml:"initial"`
	Final       []string                   `yaml:"final"`
	Inbox       []string                   `yaml:"inbox"`
	Bindings    map[string]string          `yaml:"bindings"`
	Links       map[string]string          `yaml:"links"`
	Readiness   map[string][]conditionFile `yaml:"readiness"`
	Transitions []struct {
		From    string          `yaml:"from"`
		To      string          `yaml:"to"`
		Human   bool            `yaml:"human"`
		Guards  []conditionFile `yaml:"guards"`
		Gates   []commandFile   `yaml:"gates"`
		Actions []commandFile   `yaml:"actions"`
	} `yaml:"transitions"`
}

// skillFile is the frontmatter of a SKILL.md. Changes is a pointer so a
// Skill that doesn't say whether it changes anything can be told apart.
type skillFile struct {
	Changes    *bool    `yaml:"changes"`
	Invocation string   `yaml:"invocation"`
	Guidelines []string `yaml:"guidelines"`
	Personas   []struct {
		Name     string `yaml:"name"`
		Fallback string `yaml:"fallback"`
	} `yaml:"personas"`
}

type conditionFile struct {
	Kind     string   `yaml:"kind"`
	Link     string   `yaml:"link"`
	Statuses []string `yaml:"statuses"`
	Min      int      `yaml:"min"`
}

type commandFile struct {
	Name string `yaml:"name"`
	Cmd  string `yaml:"cmd"`
}

// Load reads the Playbook of the project rooted at root, merged over its
// Base Playbook when it extends one. Artifact Types are declared in the
// lexical order of their file names, a Base Playbook's first.
func Load(root string) (*engine.Playbook, error) {
	own, err := readLayer(os.DirFS(filepath.Join(root, Dir)), Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no Playbook found: %s is missing", path.Join(Dir, "playbook.yaml"))
	}
	if err != nil {
		return nil, err
	}
	l := own
	if own.extends != nil {
		base, err := resolveBase(root, own.extends)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path.Join(Dir, "playbook.yaml"), err)
		}
		l = merge(base, own)
	}
	pb := &engine.Playbook{Name: own.name, Types: l.types, Skills: l.skills, Personas: l.personas, Guidelines: l.guidelines}
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(l.skills)) {
		problems = append(problems, l.skillProblems[name]...)
	}
	if problems = append(problems, check(pb, l.typeFiles, l.skillFiles)...); len(problems) > 0 {
		return nil, &Invalid{Problems: problems}
	}
	return pb, nil
}

// layer is one Playbook as read from its files, before it is merged with
// the Playbook extending it. Every file is named by label, the directory
// the Playbook's author knows it by, for messages.
type layer struct {
	name       string
	extends    *baseRef
	types      []*engine.ArtifactType // in declaration order
	skills     map[string]*engine.Skill
	personas   []string
	guidelines []string

	typeFiles, skillFiles map[string]string   // name -> the file declaring it
	skillProblems         map[string][]string // Skill -> what its file lacks
}

// readLayer reads the Playbook held in fsys. A missing playbook.yaml is an
// error wrapping os.ErrNotExist.
func readLayer(fsys fs.FS, label string) (*layer, error) {
	var pf playbookFile
	if err := readYAML(fsys, label, "playbook.yaml", &pf); err != nil {
		return nil, err
	}
	l := &layer{
		name:          pf.Name,
		skills:        map[string]*engine.Skill{},
		typeFiles:     map[string]string{},
		skillFiles:    map[string]string{},
		skillProblems: map[string][]string{},
	}
	if !pf.Extends.IsZero() {
		ref, err := parseBaseRef(&pf.Extends)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", in(label, "playbook.yaml"), err)
		}
		l.extends = ref
	}
	if err := l.readTypes(fsys, label); err != nil {
		return nil, err
	}
	if err := l.readSkills(fsys, label); err != nil {
		return nil, err
	}
	for dir, names := range map[string]*[]string{"personas": &l.personas, "guidelines": &l.guidelines} {
		paths, err := fs.Glob(fsys, dir+"/*.md")
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			*names = append(*names, strings.TrimSuffix(path.Base(p), ".md"))
		}
	}
	return l, nil
}

func (l *layer) readTypes(fsys fs.FS, label string) error {
	paths, err := fs.Glob(fsys, "types/*.yaml")
	if err != nil {
		return err
	}
	slices.Sort(paths)
	for _, p := range paths {
		var tf typeFile
		if err := readYAML(fsys, label, p, &tf); err != nil {
			return err
		}
		rel := in(label, p)
		if tf.Name == "" || tf.Prefix == "" {
			return fmt.Errorf("%s: an Artifact Type needs a name and a prefix", rel)
		}
		if l.typeFiles[tf.Name] != "" {
			return fmt.Errorf("%s: Artifact Type %q is declared twice", rel, tf.Name)
		}
		t := &engine.ArtifactType{
			Name:     tf.Name,
			Prefix:   tf.Prefix,
			Statuses: tf.Statuses,
			Initial:  tf.Initial,
			Final:    tf.Final,
			Inbox:    tf.Inbox,
			Bindings: tf.Bindings,
			Links:    tf.Links,
		}
		for status, cfs := range tf.Readiness {
			if t.Readiness == nil {
				t.Readiness = map[string][]engine.Condition{}
			}
			t.Readiness[status] = conditions(cfs)
		}
		for _, tr := range tf.Transitions {
			if err := checkCommands(tr.Gates, "a Gate", tr.From, tr.To); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if err := checkCommands(tr.Actions, "an Action", tr.From, tr.To); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			t.Transitions = append(t.Transitions, engine.Transition{
				From:    tr.From,
				To:      tr.To,
				Human:   tr.Human,
				Guards:  conditions(tr.Guards),
				Gates:   commands(tr.Gates),
				Actions: commands(tr.Actions),
			})
		}
		l.types = append(l.types, t)
		l.typeFiles[t.Name] = rel
	}
	return nil
}

// readSkills reads every Skill: a directory under skills/ holding a
// SKILL.md whose YAML frontmatter declares whether it changes code or
// Artifacts and its Invocation Mode. The problems of a Skill that doesn't
// are kept with it, so a Skill overriding it drops them.
func (l *layer) readSkills(fsys fs.FS, label string) error {
	paths, err := fs.Glob(fsys, "skills/*/SKILL.md")
	if err != nil {
		return err
	}
	slices.Sort(paths)
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := in(label, p)
		var sf skillFile
		if fm, ok := frontmatter(data); ok {
			if err := decodeYAML(fm, rel, &sf); err != nil {
				return err
			}
		}
		name := path.Base(path.Dir(p))
		var problems []string
		if sf.Changes == nil {
			problems = append(problems, rel+": a Skill must declare changes: true or false")
		}
		switch sf.Invocation {
		case engine.InvokedByUser, engine.InvokedByAgent, engine.InvokedByBinding:
		case "":
			problems = append(problems, rel+": a Skill must declare its Invocation Mode (invocation: user, agent or bound)")
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown Invocation Mode %q (want user, agent or bound)", rel, sf.Invocation))
		}
		s := &engine.Skill{Name: name, Changes: sf.Changes != nil && *sf.Changes, Invocation: sf.Invocation, Guidelines: sf.Guidelines}
		for _, pf := range sf.Personas {
			s.Personas = append(s.Personas, engine.PersonaRef{Name: pf.Name, Fallback: pf.Fallback})
		}
		l.skills[name] = s
		l.skillFiles[name] = rel
		l.skillProblems[name] = problems
	}
	return nil
}

// merge lays the extending Playbook own over its Base Playbook: an Artifact
// Type, Skill, Persona or Guideline own declares replaces the base's one of
// the same name, and everything else the base declares is kept. A replaced
// Artifact Type keeps the base's place in the declaration order; own's other
// Types follow the base's.
func merge(base, own *layer) *layer {
	m := &layer{
		skills:        maps.Clone(base.skills),
		typeFiles:     maps.Clone(base.typeFiles),
		skillFiles:    maps.Clone(base.skillFiles),
		skillProblems: maps.Clone(base.skillProblems),
	}
	for _, t := range base.types {
		if o := slices.IndexFunc(own.types, func(o *engine.ArtifactType) bool { return o.Name == t.Name }); o >= 0 {
			t = own.types[o]
		}
		m.types = append(m.types, t)
	}
	for _, t := range own.types {
		if !slices.ContainsFunc(base.types, func(b *engine.ArtifactType) bool { return b.Name == t.Name }) {
			m.types = append(m.types, t)
		}
	}
	maps.Copy(m.typeFiles, own.typeFiles)
	maps.Copy(m.skills, own.skills)
	maps.Copy(m.skillFiles, own.skillFiles)
	maps.Copy(m.skillProblems, own.skillProblems)
	m.personas = union(base.personas, own.personas)
	m.guidelines = union(base.guidelines, own.guidelines)
	return m
}

// union is the names in a and then those in b, each once.
func union(a, b []string) []string {
	u := slices.Clone(a)
	for _, n := range b {
		if !slices.Contains(u, n) {
			u = append(u, n)
		}
	}
	return u
}

// frontmatter returns the YAML block a Markdown file starts with, if any.
func frontmatter(data []byte) ([]byte, bool) {
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, false
	}
	if bytes.HasPrefix(rest, []byte("---\n")) {
		return nil, true
	}
	fm, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	return fm, ok
}

// in names the file name of the Playbook labelled label, for messages. A
// label may be a URL, which path.Join would mangle.
func in(label, name string) string {
	return strings.TrimSuffix(label, "/") + "/" + name
}

// readYAML decodes the file name in fsys, reporting it as label/name.
func readYAML(fsys fs.FS, label, name string, v any) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	return decodeYAML(data, in(label, name), v)
}

func decodeYAML(data []byte, file string, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelt field is an error, not silently ignored
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}

func commands(cfs []commandFile) []engine.Command {
	var cs []engine.Command
	for _, c := range cfs {
		cs = append(cs, engine.Command{Name: c.Name, Cmd: c.Cmd})
	}
	return cs
}

func conditions(cfs []conditionFile) []engine.Condition {
	var cs []engine.Condition
	for _, c := range cfs {
		cs = append(cs, engine.Condition{Kind: c.Kind, Link: c.Link, Statuses: c.Statuses, Min: c.Min})
	}
	return cs
}

// checkCommands refuses a Gate or Action that lacks a name (used to report
// it) or a cmd (what runs).
func checkCommands(cfs []commandFile, what, from, to string) error {
	for _, c := range cfs {
		if c.Name == "" || c.Cmd == "" {
			return fmt.Errorf("%s on %q → %q needs a name and a cmd", what, from, to)
		}
	}
	return nil
}
