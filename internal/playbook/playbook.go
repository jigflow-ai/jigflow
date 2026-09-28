// Package playbook loads a project's Playbook from disk: a small Playbook
// file plus one YAML file per Artifact Type.
//
//	.jigflow/playbook.yaml      the Playbook file
//	.jigflow/types/*.yaml       one file per Artifact Type
package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// Dir is the project-relative directory holding the Playbook.
const Dir = ".jigflow"

type playbookFile struct {
	Name string `yaml:"name"`
}

type typeFile struct {
	Name        string            `yaml:"name"`
	Prefix      string            `yaml:"prefix"`
	Statuses    []string          `yaml:"statuses"`
	Initial     []string          `yaml:"initial"`
	Final       []string          `yaml:"final"`
	Bindings    map[string]string `yaml:"bindings"`
	Transitions []struct {
		From    string        `yaml:"from"`
		To      string        `yaml:"to"`
		Gates   []commandFile `yaml:"gates"`
		Actions []commandFile `yaml:"actions"`
	} `yaml:"transitions"`
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
	pb := &engine.Playbook{Name: pf.Name}

	paths, err := filepath.Glob(filepath.Join(dir, "types", "*.yaml"))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
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
			Bindings: tf.Bindings,
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
				Gates:   commands(tr.Gates),
				Actions: commands(tr.Actions),
			})
		}
		pb.Types = append(pb.Types, t)
	}
	return pb, nil
}

func readYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
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
