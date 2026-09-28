// Package store keeps Artifacts where their Artifact Type says they live.
//
// The file Store keeps each Artifact as a Markdown file with YAML frontmatter
// in one committed state directory:
//
//	.jigflow/state/<id>.md
package store

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

// StateDir is the project-relative state directory of the file Store.
const StateDir = ".jigflow/state"

// ErrNotFound is returned when no Artifact has the requested id.
var ErrNotFound = errors.New("no such Artifact")

// File is the file Store of one project.
type File struct {
	dir string
}

// NewFile returns the file Store of the project rooted at root.
func NewFile(root string) *File {
	return &File{dir: filepath.Join(root, StateDir)}
}

// frontmatter is the on-disk field order of an Artifact file.
type frontmatter struct {
	ID     string `yaml:"id"`
	Type   string `yaml:"type"`
	Status string `yaml:"status"`
	Title  string `yaml:"title"`
}

const delim = "---\n"

// List returns every Artifact in the Store, in no particular order.
func (f *File) List() ([]engine.Artifact, error) {
	entries, err := os.ReadDir(f.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []engine.Artifact
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		a, _, err := f.read(filepath.Join(f.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Get returns the Artifact with the given id.
func (f *File) Get(id string) (engine.Artifact, error) {
	if !validID(id) {
		return engine.Artifact{}, fmt.Errorf("%q: %w", id, ErrNotFound)
	}
	a, _, err := f.read(f.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return engine.Artifact{}, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return a, err
}

// Save writes the Artifact's frontmatter, keeping any body already on disk.
func (f *File) Save(a engine.Artifact) error {
	if !validID(a.ID) {
		return fmt.Errorf("invalid Artifact id %q", a.ID)
	}
	body := "\n"
	if _, existing, err := f.read(f.path(a.ID)); err == nil {
		body = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fm, err := yaml.Marshal(frontmatter{ID: a.ID, Type: a.Type, Status: a.Status, Title: a.Title})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	content := delim + string(fm) + delim + body
	return os.WriteFile(f.path(a.ID), []byte(content), 0o644)
}

// validID reports whether id can name a file inside the state directory.
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

func (f *File) path(id string) string { return filepath.Join(f.dir, id+".md") }

// read parses an Artifact file into its frontmatter and body.
func (f *File) read(path string) (engine.Artifact, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return engine.Artifact{}, "", err
	}
	rest, ok := bytes.CutPrefix(data, []byte(delim))
	if !ok {
		return engine.Artifact{}, "", fmt.Errorf("%s: no frontmatter block", path)
	}
	fmText, body, ok := strings.Cut(string(rest), "\n"+delim)
	if !ok {
		return engine.Artifact{}, "", fmt.Errorf("%s: frontmatter block is not closed", path)
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return engine.Artifact{}, "", fmt.Errorf("%s: %w", path, err)
	}
	return engine.Artifact{ID: fm.ID, Type: fm.Type, Status: fm.Status, Title: fm.Title}, body, nil
}
