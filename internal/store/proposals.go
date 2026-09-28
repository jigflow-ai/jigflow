package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// ProposalDir is the project-relative directory where Proposals are kept, one
// YAML file per Proposal, pending or decided:
//
//	.jigflow/proposals/<id>.yaml
const ProposalDir = ".jigflow/proposals"

// ErrNoProposal is returned when no Proposal has the requested id.
var ErrNoProposal = errors.New("no such Proposal")

// Proposals is the Proposal store of one project.
type Proposals struct {
	dir string
}

// NewProposals returns the Proposal store of the project rooted at root.
func NewProposals(root string) *Proposals {
	return &Proposals{dir: filepath.Join(root, ProposalDir)}
}

// proposalFile is the on-disk field order of a Proposal file.
type proposalFile struct {
	ID      string     `yaml:"id"`
	By      string     `yaml:"by,omitempty"`
	Status  string     `yaml:"status"`
	Summary string     `yaml:"summary"`
	Items   []itemFile `yaml:"items"`
}

// itemFile is one Proposal item, as agents write it and as it is kept.
type itemFile struct {
	Create string              `yaml:"create,omitempty"`
	Ref    string              `yaml:"ref,omitempty"`
	Title  string              `yaml:"title,omitempty"`
	Status string              `yaml:"status,omitempty"`
	Links  map[string][]string `yaml:"links,omitempty"`
	Move   string              `yaml:"move,omitempty"`
	To     string              `yaml:"to,omitempty"`
}

// ParseProposal reads a Proposal as an agent writes it: a summary and its
// items.
//
//	summary: break S-1 into tickets
//	items:
//	  - {create: Ticket, ref: table, title: Reset-token table, links: {part_of: [S-1]}}
//	  - {create: Ticket, title: Reset endpoint, links: {blocked_by: [table]}}
//	  - {move: S-1, to: ticketed}
func ParseProposal(data []byte) (summary string, items []engine.ProposalItem, err error) {
	var in struct {
		Summary string     `yaml:"summary"`
		Items   []itemFile `yaml:"items"`
	}
	if err := decode(data, &in); err != nil {
		return "", nil, err
	}
	return in.Summary, fromItemFiles(in.Items), nil
}

// List returns every Proposal, in no particular order.
func (s *Proposals) List() ([]engine.Proposal, error) {
	paths, err := filepath.Glob(filepath.Join(s.dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	var out []engine.Proposal
	for _, path := range paths {
		p, err := s.read(path)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Get returns the Proposal with the given id.
func (s *Proposals) Get(id string) (engine.Proposal, error) {
	if !validID(id) {
		return engine.Proposal{}, fmt.Errorf("%q: %w", id, ErrNoProposal)
	}
	p, err := s.read(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return engine.Proposal{}, fmt.Errorf("%s: %w", id, ErrNoProposal)
	}
	return p, err
}

// Save writes the Proposal.
func (s *Proposals) Save(p engine.Proposal) error {
	if !validID(p.ID) {
		return fmt.Errorf("invalid Proposal id %q", p.ID)
	}
	pf := proposalFile{ID: p.ID, By: p.By, Status: p.Status, Summary: p.Summary}
	for _, it := range p.Items {
		pf.Items = append(pf.Items, itemFile(it))
	}
	data, err := yaml.Marshal(pf)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path(p.ID), data, 0o644)
}

func (s *Proposals) path(id string) string { return filepath.Join(s.dir, id+".yaml") }

func (s *Proposals) read(path string) (engine.Proposal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return engine.Proposal{}, err
	}
	var pf proposalFile
	if err := decode(data, &pf); err != nil {
		return engine.Proposal{}, fmt.Errorf("%s: %w", path, err)
	}
	return engine.Proposal{ID: pf.ID, By: pf.By, Status: pf.Status, Summary: pf.Summary, Items: fromItemFiles(pf.Items)}, nil
}

func fromItemFiles(ifs []itemFile) []engine.ProposalItem {
	var items []engine.ProposalItem
	for _, it := range ifs {
		items = append(items, engine.ProposalItem(it))
	}
	return items
}

// decode unmarshals YAML, refusing fields it doesn't know.
func decode(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
