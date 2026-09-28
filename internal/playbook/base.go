package playbook

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// baseRef is where a Playbook's single Base Playbook comes from: exactly
// one of a Playbook built into the binary, a directory at a local path
// (relative to the project root), or a git repository pinned to a ref.
type baseRef struct {
	Builtin string `yaml:"builtin"`
	Path    string `yaml:"path"`
	Git     string `yaml:"git"`
	Ref     string `yaml:"ref"`
}

// parseBaseRef reads the extends of a Playbook file, which names a single
// Base Playbook: combining several isn't supported.
func parseBaseRef(n *yaml.Node) (*baseRef, error) {
	if n.Kind == yaml.SequenceNode {
		return nil, fmt.Errorf("extends lists %d Base Playbooks, but a Playbook extends a single Base Playbook", len(n.Content))
	}
	// A misspelt key is an error, as everywhere else in a Playbook; a
	// Node's Decode doesn't check for them.
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			switch k := n.Content[i].Value; k {
			case "builtin", "path", "git", "ref":
			default:
				return nil, fmt.Errorf("extends: line %d: unknown field %q (want builtin, path or git, and a ref for git)", n.Content[i].Line, k)
			}
		}
	}
	var ref baseRef
	if err := n.Decode(&ref); err != nil {
		return nil, fmt.Errorf("extends: %w", err)
	}
	sources := 0
	for _, s := range []string{ref.Builtin, ref.Path, ref.Git} {
		if s != "" {
			sources++
		}
	}
	switch {
	case sources != 1:
		return nil, fmt.Errorf("extends names one of builtin, path or git, the single Base Playbook")
	case ref.Git != "" && ref.Ref == "":
		return nil, fmt.Errorf("a git Base Playbook must be pinned to a ref (extends: {git: %s, ref: <tag, branch or commit>})", ref.Git)
	case ref.Git == "" && ref.Ref != "":
		return nil, fmt.Errorf("only a git Base Playbook is pinned to a ref")
	}
	return &ref, nil
}

// builtin holds the Playbooks built into the binary, one directory each.
//
//go:embed builtin
var builtin embed.FS

// resolveBase reads the Base Playbook ref names for the project at root.
func resolveBase(root string, ref *baseRef) (*layer, error) {
	switch {
	case ref.Builtin != "":
		return builtinBase(ref.Builtin)
	case ref.Git != "":
		return gitBase(root, ref)
	}
	dir := ref.Path
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return readBase(os.DirFS(dir), ref.Path)
}

// readBase reads a Base Playbook, labelled for messages as label.
func readBase(fsys fs.FS, label string) (*layer, error) {
	l, err := readLayer(fsys, label)
	if err != nil {
		return nil, fmt.Errorf("Base Playbook %s: %w", label, err)
	}
	if l.extends != nil {
		return nil, fmt.Errorf("Base Playbook %s extends another, but a Playbook extends a single Base Playbook", label)
	}
	return l, nil
}

// builtinBase reads the Playbook named name built into the binary.
func builtinBase(name string) (*layer, error) {
	entries, err := builtin.ReadDir("builtin")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Name() == name {
			fsys, err := fs.Sub(builtin, "builtin/"+name)
			if err != nil {
				return nil, err
			}
			return readBase(fsys, "builtin:"+name)
		}
		names = append(names, e.Name())
	}
	return nil, fmt.Errorf("no Playbook %q is built in (built in: %s)", name, strings.Join(names, ", "))
}

// Builtin reports whether a Playbook named name is built into the binary.
func Builtin(name string) bool {
	_, err := fs.Stat(builtin, "builtin/"+name+"/playbook.yaml")
	return err == nil
}

// BuiltinSkill returns the SKILL.md of the Skill named name that a
// Playbook built into the binary ships, if any does.
func BuiltinSkill(name string) ([]byte, bool) {
	paths, _ := fs.Glob(builtin, "builtin/*/skills/"+name+"/SKILL.md")
	if len(paths) == 0 {
		return nil, false
	}
	data, err := fs.ReadFile(builtin, paths[0])
	return data, err == nil
}
