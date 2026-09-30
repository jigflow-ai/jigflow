package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// Apply writes the items of an approved Proposal that change the Playbook
// into the project rooted at root: a Gate's command or the Mockup folder
// into the Playbook file, or out of it, which keeps the rest of what it says, a Guideline, an Artifact Type or a
// Skill into its own file, overriding the Base Playbook's of the same name.
// It applies all of them or none: when one can't be, or the Playbook they
// make fails its checks or verify, it puts every file back as it was.
func Apply(root string, items []engine.ProposalItem, verify func(*engine.Playbook) error) (err error) {
	files, err := changes(root, items)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, Dir)
	saved := map[string][]byte{} // path -> its content before, nil if it didn't exist
	defer func() {
		if err == nil {
			return
		}
		for path, data := range saved {
			if data != nil {
				os.WriteFile(path, data, 0o644)
				continue
			}
			os.Remove(path)
			// Remove the directories writing it made, and only those.
			for d := filepath.Dir(path); d != dir && os.Remove(d) == nil; d = filepath.Dir(d) {
			}
		}
	}()
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		saved[path] = data
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[rel], 0o644); err != nil {
			return err
		}
	}
	pb, err := Load(root)
	if err != nil {
		return err
	}
	return verify(pb)
}

// Candidate loads the Playbook of the project rooted at root as the items
// of a Proposal would make it once approved, writing nothing, so that a
// Playbook can be checked and simulated before a person approves it.
func Candidate(root string, items []engine.ProposalItem) (*engine.Playbook, error) {
	files, err := changes(root, items)
	if err != nil {
		return nil, err
	}
	own := os.DirFS(filepath.Join(root, Dir))
	fsys := fstest.MapFS{}
	for _, top := range []string{"playbook.yaml", "types", "skills", "personas", "guidelines", "migrations"} {
		err := fs.WalkDir(own, top, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == top && errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(own, p)
			if err != nil {
				return err
			}
			fsys[p] = &fstest.MapFile{Data: data}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for rel, data := range files {
		fsys[rel] = &fstest.MapFile{Data: data}
	}
	return load(root, fsys)
}

// changes returns the files, by their path in Dir, that the items write,
// each with its content once they are all written.
func changes(root string, items []engine.ProposalItem) (map[string][]byte, error) {
	dir := filepath.Join(root, Dir)
	files := map[string][]byte{}
	for _, it := range items {
		switch {
		case it.Gate != "" || it.Mockups != "":
			data, ok := files["playbook.yaml"]
			if !ok {
				var err error
				if data, err = os.ReadFile(filepath.Join(dir, "playbook.yaml")); err != nil {
					return nil, err
				}
			}
			f, err := parseFile(root, data)
			if err != nil {
				return nil, err
			}
			if err := setValue(f, it); err != nil {
				return nil, err
			}
			if files["playbook.yaml"], err = f.encode(); err != nil {
				return nil, err
			}
		case it.Guideline != "":
			rel := path.Join("guidelines", it.Guideline+".md")
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
				return nil, fmt.Errorf("Guideline %q already exists, in %s; edit that file instead", it.Guideline, path.Join(Dir, rel))
			}
			files[rel] = []byte(it.Text)
		case it.Type != "":
			rel, err := typePath(dir, it)
			if err != nil {
				return nil, err
			}
			files[rel] = []byte(it.Text)
		case it.Skill != "":
			files[path.Join("skills", it.Skill, "SKILL.md")] = []byte(it.Text)
		}
	}
	return files, nil
}

// setValue makes the Playbook file f say the value the item gives, or
// nothing where the item removes it.
func setValue(f *File, it engine.ProposalItem) error {
	file := path.Join(Dir, "playbook.yaml")
	switch {
	case it.Gate != "" && it.Remove:
		if !f.Remove("gates", it.Gate) {
			return fmt.Errorf("%s gives Gate %q no command to remove", file, it.Gate)
		}
	case it.Gate != "":
		return f.Set(it.Cmd, "gates", it.Gate)
	case it.Remove:
		// It names the folder it removes, so that it removes no other.
		var own string
		if n := lookup(f.doc.Content[0], "mockups"); n != nil {
			own = n.Value
		}
		if own == "" || path.Clean(own) != path.Clean(it.Mockups) {
			return fmt.Errorf("%s gives no Mockup folder %s to remove", file, it.Mockups)
		}
		f.Remove("mockups")
	default:
		return f.Set(it.Mockups, "mockups")
	}
	return nil
}

// typePath is the path in dir, the project's Playbook, of the file the
// Artifact Type the item declares is written to: the file of the project's
// that declares it already, which it replaces, or else one named after it.
func typePath(dir string, it engine.ProposalItem) (string, error) {
	var declared struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal([]byte(it.Text), &declared); err != nil {
		return "", fmt.Errorf("Artifact Type %q: %w", it.Type, err)
	}
	if declared.Name != it.Type {
		return "", fmt.Errorf("the text of Artifact Type %q declares %q: its name must be the item's", it.Type, declared.Name)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "types", "*.yaml"))
	if err != nil {
		return "", err
	}
	named := strings.ToLower(strings.ReplaceAll(it.Type, " ", "-")) + ".yaml"
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		var other struct {
			Name string `yaml:"name"`
		}
		_ = yaml.Unmarshal(data, &other)
		switch {
		case other.Name == it.Type:
			return path.Join("types", filepath.Base(p)), nil
		case filepath.Base(p) == named:
			return "", fmt.Errorf("Artifact Type %q would be written to %s, which declares %q", it.Type, path.Join(Dir, "types", named), other.Name)
		}
	}
	return path.Join("types", named), nil
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
	data, err := os.ReadFile(filepath.Join(root, Dir, "playbook.yaml"))
	if err != nil {
		return nil, err
	}
	return parseFile(root, data)
}

// parseFile reads data as the Playbook file of the project rooted at root.
func parseFile(root string, data []byte) (*File, error) {
	f := &File{root: root}
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
				// The value is replaced, the comments on it kept.
				v.HeadComment, v.LineComment, v.FootComment = child.HeadComment, child.LineComment, child.FootComment
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

// Remove makes the file say nothing at the path of keys, and drops each
// mapping on the way that is left empty, reporting whether it said
// something there.
func (f *File) Remove(keys ...string) bool {
	nodes := []*yaml.Node{f.doc.Content[0]}
	for _, k := range keys {
		n := lookup(nodes[len(nodes)-1], k)
		if n == nil {
			return false
		}
		nodes = append(nodes, n)
	}
	for i := len(keys) - 1; i >= 0; i-- {
		m := nodes[i]
		for j := 0; j+1 < len(m.Content); j += 2 {
			if m.Content[j].Value == keys[i] {
				m.Content = slices.Delete(m.Content, j, j+2)
				break
			}
		}
		if i == 0 || len(m.Content) > 0 {
			break
		}
	}
	f.changed = true
	return true
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
	data, err := f.encode()
	if err != nil {
		return err
	}
	return os.WriteFile(f.path(), data, 0o644)
}

// encode is what the file says, as YAML.
func (f *File) encode() ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&f.doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
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
