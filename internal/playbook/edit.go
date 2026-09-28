package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// Apply writes the items of an approved Proposal that change the Playbook
// into the project rooted at root: a Gate's command into the Playbook file,
// which keeps the rest of what it says, and a Guideline into its own file,
// overriding the Base Playbook's of the same name. It applies all of them
// or none: when one can't be, or the Playbook they make fails its checks,
// it puts every file back as it was.
func Apply(root string, items []engine.ProposalItem) (err error) {
	saved := map[string][]byte{} // path -> its content before, nil if it didn't exist
	save := func(path string) error {
		if _, ok := saved[path]; ok {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		saved[path] = data
		return nil
	}
	defer func() {
		if err == nil {
			return
		}
		for path, data := range saved {
			if data == nil {
				os.Remove(path)
			} else {
				os.WriteFile(path, data, 0o644)
			}
		}
	}()
	pbFile := filepath.Join(root, Dir, "playbook.yaml")
	for _, it := range items {
		switch {
		case it.Gate != "":
			if err := save(pbFile); err != nil {
				return err
			}
			f, err := OpenFile(root)
			if err != nil {
				return err
			}
			if err := f.Set(it.Cmd, "gates", it.Gate); err != nil {
				return err
			}
			if err := f.Save(); err != nil {
				return err
			}
		case it.Guideline != "":
			path := filepath.Join(root, Dir, "guidelines", it.Guideline+".md")
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("Guideline %q already exists, in %s; edit that file instead", it.Guideline, filepath.Join(Dir, "guidelines", it.Guideline+".md"))
			}
			if err := save(path); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(it.Text), 0o644); err != nil {
				return err
			}
		}
	}
	_, err = Load(root)
	return err
}

// File is the project's Playbook file, open for changing what it says
// while keeping its comments and the rest of it.
type File struct {
	root    string
	doc     yaml.Node
	changed bool
}

// OpenFile opens the Playbook file of the project rooted at root.
func OpenFile(root string) (*File, error) {
	f := &File{root: root}
	data, err := os.ReadFile(f.path())
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &f.doc); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(Dir, "playbook.yaml"), err)
	}
	if f.doc.Kind == 0 {
		f.doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	return f, nil
}

func (f *File) path() string { return filepath.Join(f.root, Dir, "playbook.yaml") }

// Has reports whether the file says something at the path of keys.
func (f *File) Has(keys ...string) bool {
	n := f.doc.Content[0]
	for _, k := range keys {
		if n = lookup(n, k); n == nil {
			return false
		}
	}
	return true
}

// Set makes the file say value at the path of keys, making the mappings on
// the way that it lacks.
func (f *File) Set(value any, keys ...string) error {
	var v yaml.Node
	if err := v.Encode(value); err != nil {
		return err
	}
	n := f.doc.Content[0]
	for i, k := range keys {
		if n.Kind != yaml.MappingNode {
			return fmt.Errorf("%s: %s isn't a mapping", filepath.Join(Dir, "playbook.yaml"), strings.Join(keys[:i], "."))
		}
		child := lookup(n, k)
		if i == len(keys)-1 {
			if child != nil {
				*child = v
			} else {
				n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &v)
			}
			break
		}
		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, child)
		}
		n = child
	}
	f.changed = true
	return nil
}

// DeclareConnector makes the file declare the Connector c, as its Base
// Playbook does, unless it declares one of that name already: the project's
// settings for a Connector replace the Base Playbook's whole.
func (f *File) DeclareConnector(c *engine.Connector) error {
	if f.Has("connectors", c.Name) {
		return nil
	}
	type typeFile struct {
		Settings map[string]any            `yaml:"settings,omitempty"`
		Statuses map[string]any            `yaml:"statuses,omitempty"`
		Fields   map[string]map[string]any `yaml:"fields,omitempty"`
	}
	cf := struct {
		Command  string              `yaml:"command"`
		Args     []string            `yaml:"args,omitempty"`
		Marker   string              `yaml:"marker,omitempty"`
		Settings map[string]any      `yaml:"settings,omitempty"`
		Types    map[string]typeFile `yaml:"types,omitempty"`
	}{Command: c.Command, Args: c.Args, Settings: c.Settings}
	if c.Marker != DefaultMarker {
		cf.Marker = c.Marker
	}
	for name, m := range c.Types {
		tf := typeFile{Settings: m.Settings}
		for status, term := range m.Statuses {
			if tf.Statuses == nil {
				tf.Statuses = map[string]any{}
			}
			tf.Statuses[status] = termValue(term)
		}
		for field, values := range m.Fields {
			if tf.Fields == nil {
				tf.Fields = map[string]map[string]any{}
			}
			tf.Fields[field] = map[string]any{}
			for value, term := range values {
				tf.Fields[field][value] = termValue(term)
			}
		}
		if cf.Types == nil {
			cf.Types = map[string]typeFile{}
		}
		cf.Types[name] = tf
	}
	return f.Set(cf, "connectors", c.Name)
}

// termValue is a label or state as a Playbook file writes it: the label's
// name, or a mapping naming a label, a state, or both.
func termValue(t engine.TrackerTerm) any {
	if t.State == "" {
		return t.Label
	}
	m := map[string]string{"state": t.State}
	if t.Label != "" {
		m["label"] = t.Label
	}
	return m
}

// Changed reports whether anything was set.
func (f *File) Changed() bool { return f.changed }

// Save writes the file back, if anything was set.
func (f *File) Save() error {
	if !f.changed {
		return nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&f.doc); err != nil {
		return err
	}
	return os.WriteFile(f.path(), out.Bytes(), 0o644)
}

// lookup returns the value of key in the mapping n, or nil.
func lookup(n *yaml.Node, key string) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
