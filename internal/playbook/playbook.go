// Package playbook loads a project's Playbook from disk: a small Playbook
// file plus one YAML file per Artifact Type.
//
//	.jigflow/playbook.yaml      the Playbook file
//	.jigflow/types/*.yaml       one file per Artifact Type
//	.jigflow/skills/*/SKILL.md  one directory per Skill, named after it
//	.jigflow/personas/*.md      one file per Persona, named after it
//
// A Playbook that fails any check doesn't load: Load reports every problem
// at once, so `jfl check` and every other command print the same list.
package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// Dir is the project-relative directory holding the Playbook.
const Dir = ".jigflow"

type playbookFile struct {
	Name string `yaml:"name"`
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
	Changes    *bool  `yaml:"changes"`
	Invocation string `yaml:"invocation"`
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

// Load reads the Playbook of the project rooted at root. Artifact Types are
// declared in the lexical order of their file names.
func Load(root string) (*engine.Playbook, error) {
	dir := filepath.Join(root, Dir)
	var pf playbookFile
	if err := readYAML(filepath.Join(dir, "playbook.yaml"), &pf); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no Playbook found: %s is missing", filepath.Join(Dir, "playbook.yaml"))
		}
		return nil, err
	}
	pb := &engine.Playbook{Name: pf.Name, Skills: map[string]*engine.Skill{}}

	paths, err := filepath.Glob(filepath.Join(dir, "types", "*.yaml"))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	files := map[string]string{} // Artifact Type -> its file, for messages
	for _, path := range paths {
		var tf typeFile
		if err := readYAML(path, &tf); err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, path)
		if tf.Name == "" || tf.Prefix == "" {
			return nil, fmt.Errorf("%s: an Artifact Type needs a name and a prefix", rel)
		}
		if pb.Type(tf.Name) != nil {
			return nil, fmt.Errorf("%s: Artifact Type %q is declared twice", rel, tf.Name)
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
				return nil, fmt.Errorf("%s: %w", rel, err)
			}
			if err := checkCommands(tr.Actions, "an Action", tr.From, tr.To); err != nil {
				return nil, fmt.Errorf("%s: %w", rel, err)
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
		pb.Types = append(pb.Types, t)
		files[t.Name] = rel
	}
	skillFiles, problems, err := loadSkills(root, pb)
	if err != nil {
		return nil, err
	}
	personas, err := filepath.Glob(filepath.Join(dir, "personas", "*.md"))
	if err != nil {
		return nil, err
	}
	for _, path := range personas {
		pb.Personas = append(pb.Personas, strings.TrimSuffix(filepath.Base(path), ".md"))
	}
	if problems = append(problems, check(pb, files, skillFiles)...); len(problems) > 0 {
		return nil, &Invalid{Problems: problems}
	}
	return pb, nil
}

// loadSkills reads every Skill: a directory under skills/ holding a SKILL.md
// whose YAML frontmatter declares whether it changes code or Artifacts and
// its Invocation Mode. It returns each Skill's file, for messages, and the
// problems of Skills that don't.
func loadSkills(root string, pb *engine.Playbook) (map[string]string, []string, error) {
	paths, err := filepath.Glob(filepath.Join(root, Dir, "skills", "*", "SKILL.md"))
	if err != nil {
		return nil, nil, err
	}
	files := map[string]string{}
	slices.Sort(paths)
	var problems []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		var sf skillFile
		if fm, ok := frontmatter(data); ok {
			if err := decodeYAML(fm, path, &sf); err != nil {
				return nil, nil, err
			}
		}
		rel, _ := filepath.Rel(root, path)
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
		name := filepath.Base(filepath.Dir(path))
		s := &engine.Skill{Name: name, Changes: sf.Changes != nil && *sf.Changes, Invocation: sf.Invocation}
		for _, pf := range sf.Personas {
			s.Personas = append(s.Personas, engine.PersonaRef{Name: pf.Name, Fallback: pf.Fallback})
		}
		pb.Skills[name] = s
		files[name] = rel
	}
	return files, problems, nil
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

func readYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeYAML(data, path, v)
}

func decodeYAML(data []byte, path string, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelt field is an error, not silently ignored
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", path, err)
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
