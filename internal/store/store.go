package store

import (
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// Store is where the Artifacts of the Playbook's Artifact Types live: the
// file Store, or an outside tracker reached through a Connector. The CLI is
// the only writer of either (ADR 0002).
type Store interface {
	// List returns every Artifact, in no particular order.
	List() ([]engine.Artifact, error)
	// Get returns the Artifact with the given id, or an error wrapping
	// ErrNotFound.
	Get(id string) (engine.Artifact, error)
	// Create keeps a new Artifact by is creating, and returns it as kept: a
	// tracker gives it its own id.
	Create(a engine.Artifact, by engine.Actor) (engine.Artifact, error)
	// Save records a new Status or Claim of an Artifact already kept.
	Save(a engine.Artifact) error
	// Verify re-validates the Artifact with the given id before it is
	// changed: it reports whether its body was edited outside the CLI,
	// which is allowed, and refuses a change to what only the CLI may
	// change.
	Verify(id string) (bodyEdited bool, err error)
	// Comment adds text by by to the Artifact with the given id.
	Comment(id, text string, by engine.Actor) error
	// Text returns the prose of the Artifact with the given id: its body
	// and its comments, which only jfl's readers need.
	Text(id string) (Text, error)
}

// Text is an Artifact's prose, which anyone edits: its body, and the
// comments a tracker keeps apart from it (a file keeps them in its body).
type Text struct {
	Body     string
	Comments []string
}

// Open returns the Store of the project rooted at root for the Playbook pb:
// each Artifact is kept in the Store its Artifact Type declares.
func Open(root string, pb *engine.Playbook) Store {
	s := &byType{pb: pb, files: NewFile(root), connectors: map[string]*Connector{}}
	for name, c := range pb.Connectors {
		s.connectors[name] = NewConnector(root, pb, c)
	}
	return s
}

// byType is the Store of a Playbook whose Artifact Types may keep their
// Artifacts in different Stores.
type byType struct {
	pb         *engine.Playbook
	files      *File
	connectors map[string]*Connector
}

func (s *byType) List() ([]engine.Artifact, error) {
	files, err := s.files.List()
	if err != nil {
		return nil, err
	}
	// A file of an Artifact Type kept by a Connector isn't one of its
	// Artifacts.
	all := slices.DeleteFunc(files, func(a engine.Artifact) bool {
		t := s.pb.Type(a.Type)
		return t != nil && s.connectors[t.Store] != nil
	})
	for _, name := range slices.Sorted(maps.Keys(s.connectors)) {
		kept, err := s.connectors[name].List()
		if err != nil {
			return nil, err
		}
		all = append(all, kept...)
	}
	return all, nil
}

func (s *byType) Get(id string) (engine.Artifact, error) { return s.forID(id).Get(id) }

func (s *byType) Create(a engine.Artifact, by engine.Actor) (engine.Artifact, error) {
	return s.forType(a.Type).Create(a, by)
}

func (s *byType) Save(a engine.Artifact) error { return s.forType(a.Type).Save(a) }

func (s *byType) Verify(id string) (bool, error) { return s.forID(id).Verify(id) }

func (s *byType) Comment(id, text string, by engine.Actor) error {
	return s.forID(id).Comment(id, text, by)
}

func (s *byType) Text(id string) (Text, error) { return s.forID(id).Text(id) }

// forType is the Store keeping the Artifacts of the named Artifact Type.
func (s *byType) forType(name string) Store {
	if t := s.pb.Type(name); t != nil && s.connectors[t.Store] != nil {
		return s.connectors[t.Store]
	}
	return s.files
}

// forID is the Store keeping the Artifact with the given id, found by the
// prefix of its Artifact Type.
func (s *byType) forID(id string) Store {
	for _, t := range s.pb.Types {
		if c := s.connectors[t.Store]; c != nil && strings.HasPrefix(id, t.Prefix+"-") {
			return c
		}
	}
	return s.files
}
