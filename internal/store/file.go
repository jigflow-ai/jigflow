// Package store keeps Artifacts where their Artifact Type says they live:
// in the file Store, or in an outside tracker through the Connector Store
// (see connector.go). Open returns the Store of a Playbook, which routes
// each Artifact to its Artifact Type's.
//
// The file Store keeps each Artifact as a Markdown file with YAML frontmatter
// in one committed state directory:
//
//	.jigflow/state/<id>.md
//
// The last frontmatter line is a content hash of what the Store wrote, so
// edits made outside the CLI can be detected: bodies are free to edit, but
// only the CLI may change Statuses and frontmatter (ADR 0002). An Artifact's
// Claim is a frontmatter field, so it is shared through git.
package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	ID     string              `yaml:"id"`
	Type   string              `yaml:"type"`
	Status string              `yaml:"status"`
	Title  string              `yaml:"title"`
	Fields map[string]string   `yaml:"fields,omitempty"`
	Links  map[string][]string `yaml:"links,omitempty"`
	Claim  string              `yaml:"claim,omitempty"`
	// Hash is the content hash the Store wrote, always the last line of
	// the block; see contentHash.
	Hash string `yaml:"hash,omitempty"`
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

// Body returns the body of the Artifact with the given id: the Markdown
// after its frontmatter, which people and agents may edit.
func (f *File) Body(id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("%q: %w", id, ErrNotFound)
	}
	_, body, err := f.read(f.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return body, err
}

// Create writes a new Artifact's file, with the id it was given.
func (f *File) Create(a engine.Artifact, _ engine.Actor) (engine.Artifact, error) {
	return a, f.Save(a)
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
	return f.write(a, body)
}

// Comment appends text by by to the Artifact's body, after re-validating
// the file as a move does: rewriting it would otherwise make an edit to its
// frontmatter made outside the CLI look like the Store's.
func (f *File) Comment(id, text string, by engine.Actor) error {
	if _, err := f.Verify(id); err != nil {
		return err
	}
	a, body, err := f.read(f.path(id))
	if err != nil {
		return err
	}
	who := "**Comment:**"
	if by.Agent() {
		who = "**Comment by agent session " + by.Session + ":**"
	}
	return f.write(a, strings.TrimRight(body, "\n")+"\n\n"+who+"\n\n"+strings.TrimRight(text, "\n")+"\n")
}

// write writes the Artifact's file with the given body.
func (f *File) write(a engine.Artifact, body string) error {
	fm, err := yaml.Marshal(frontmatter{ID: a.ID, Type: a.Type, Status: a.Status, Title: a.Title, Fields: a.Fields, Links: a.Links, Claim: a.Claim})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	content := delim + string(fm) + hashKey + " " + contentHash(string(fm), body) + "\n" + delim + body
	return os.WriteFile(f.path(a.ID), []byte(content), 0o644)
}

const hashKey = "hash:"

// contentHash is the content hash of an Artifact file whose frontmatter,
// without its hash line, is fm. It hashes the frontmatter and the body
// separately, so an edit made outside the CLI can be told apart as touching
// the body only or the frontmatter too.
func contentHash(fm, body string) string { return fmDigest(fm) + " " + bodyDigest(body) }

func fmDigest(fm string) string     { return "frontmatter=" + digest(fm) }
func bodyDigest(body string) string { return "body=" + digest(body) }

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// EditedOutsideError reports an Artifact whose frontmatter is not what the
// Store last wrote: only the CLI may change Statuses and frontmatter.
type EditedOutsideError struct {
	ID     string
	Reason string
}

func (e *EditedOutsideError) Error() string {
	return fmt.Sprintf("%s: %s; only jfl may change Statuses and frontmatter — restore the file and use jfl move", e.ID, e.Reason)
}

// Verify re-validates the Artifact with the given id against what the Store
// last wrote to its file. It reports whether the body was edited outside the
// CLI, which is allowed, and returns an *EditedOutsideError when the
// frontmatter was, which is not.
func (f *File) Verify(id string) (bodyEdited bool, err error) {
	if !validID(id) {
		return false, fmt.Errorf("%q: %w", id, ErrNotFound)
	}
	pf, err := f.parse(f.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if err != nil {
		return false, err
	}
	if pf.hash == "" {
		return false, &EditedOutsideError{ID: id, Reason: "its file has no content hash, so it was not written by jfl"}
	}
	fmHash, bodyHash, _ := strings.Cut(pf.hash, " ")
	if fmHash != fmDigest(pf.fm) {
		return false, &EditedOutsideError{ID: id, Reason: "its frontmatter was changed outside jfl"}
	}
	if pf.artifact.ID != id {
		// A file copied or renamed keeps a valid hash but names another Artifact.
		return false, &EditedOutsideError{ID: id, Reason: fmt.Sprintf("its frontmatter id %s is not %s", pf.artifact.ID, id)}
	}
	return bodyHash != bodyDigest(pf.body), nil
}

// validID reports whether id can name a file inside the state directory.
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

func (f *File) path(id string) string { return filepath.Join(f.dir, id+".md") }

// file is an Artifact file as parsed from disk.
type file struct {
	artifact engine.Artifact
	body     string
	fm       string // the frontmatter block without its hash line
	hash     string // the content hash recorded in it, if any
}

// read parses an Artifact file into its frontmatter and body.
func (f *File) read(path string) (engine.Artifact, string, error) {
	pf, err := f.parse(path)
	return pf.artifact, pf.body, err
}

func (f *File) parse(path string) (file, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return file{}, err
	}
	rest, ok := bytes.CutPrefix(data, []byte(delim))
	if !ok {
		return file{}, fmt.Errorf("%s: no frontmatter block", path)
	}
	fmText, body, ok := strings.Cut(string(rest), "\n"+delim)
	if !ok {
		// A body emptied in an editor can take the delimiter's newline with it.
		fmText, ok = strings.CutSuffix(string(rest), "\n"+strings.TrimSuffix(delim, "\n"))
	}
	if !ok {
		return file{}, fmt.Errorf("%s: frontmatter block is not closed", path)
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return file{}, fmt.Errorf("%s: %w", path, err)
	}
	// The hash line is the last line the Store writes in the block; anything
	// else, including a hash line moved elsewhere, is frontmatter.
	unhashed := fmText + "\n"
	if i := strings.LastIndex(fmText, "\n") + 1; strings.HasPrefix(fmText[i:], hashKey) {
		unhashed = fmText[:i]
	}
	return file{
		artifact: engine.Artifact{ID: fm.ID, Type: fm.Type, Status: fm.Status, Title: fm.Title, Fields: fm.Fields, Links: fm.Links, Claim: fm.Claim},
		body:     body,
		fm:       unhashed,
		hash:     fm.Hash,
	}, nil
}
