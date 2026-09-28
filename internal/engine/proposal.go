package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Proposal statuses.
const (
	Pending  = "pending"
	Approved = "approved"
	Rejected = "rejected"
)

// Proposal is a set of creations and Transitions put forward together, which
// a person approves or rejects as one unit.
type Proposal struct {
	ID      string
	By      string // the agent session that proposed it; empty for a person
	Status  string // Pending, Approved or Rejected
	Summary string
	Items   []ProposalItem
}

// ProposalItem is one change in a Proposal: either the creation of an
// Artifact of the Type Create, or the Transition of the Artifact Move to the
// Status To.
//
// A creation's id is allocated only on approval, so other items of the same
// Proposal refer to it by its Ref, in their Links or as the Artifact they
// move.
type ProposalItem struct {
	Create string
	Ref    string
	Title  string
	Status string              // empty means the Type's first initial Status
	Links  map[string][]string // Link name -> ids or refs
	Move   string              // an id or a ref
	To     string
}

// String describes the item for a person deciding on it.
func (it ProposalItem) String() string {
	if it.Create != "" {
		s := fmt.Sprintf("create %s %q", it.Create, it.Title)
		if it.Status != "" {
			s += " in " + it.Status
		}
		for _, name := range slices.Sorted(maps.Keys(it.Links)) {
			s += fmt.Sprintf(", %s %s", name, strings.Join(it.Links[name], ", "))
		}
		return s
	}
	return fmt.Sprintf("move %s → %s", it.Move, it.To)
}

// Change is one item of an approved Proposal, as applied.
type Change struct {
	Item       int        // index of the item in the Proposal
	Artifact   Artifact   // the Artifact after the change
	From       string     // a Transition's Status before it; empty for a creation
	Transition Transition // the Transition taken, for a Transition
}

// Created reports whether the change is a creation.
func (c Change) Created() bool { return c.From == "" }

// Propose decides a new pending Proposal by actor. It is refused unless every
// item would apply to the Artifacts in all as things stand, so a mistake is
// reported to the agent now rather than to the person approving it later.
// existing is every Proposal already recorded, used to allocate its id.
func Propose(pb *Playbook, actor Actor, summary string, items []ProposalItem, all []Artifact, existing []Proposal) (Proposal, error) {
	if strings.TrimSpace(summary) == "" {
		return Proposal{}, errors.New("a Proposal needs a summary")
	}
	p := Proposal{By: actor.Session, Status: Pending, Summary: summary, Items: items}
	if _, err := Approve(pb, p, all); err != nil {
		return Proposal{}, err
	}
	highest := 0
	for _, o := range existing {
		if n, ok := idNumber("P", o.ID); ok && n > highest {
			highest = n
		}
	}
	p.ID = fmt.Sprintf("P-%d", highest+1)
	return p, nil
}

// Approve decides applying every item of p, in order, to the Artifacts in all.
// Approval is a person's, so its items may make Human Transitions and create
// into Statuses that have a Binding; Readiness doesn't hold them either, but
// Guards do, and each item sees the changes of the items before it.
//
// It returns every change, or the first item that can't be applied, in which
// case none of them may be: a Proposal applies all its changes or none. The
// engine performs no I/O: the caller runs the Gates of every Transition, saves
// every changed Artifact only if they all succeed, and then runs the Actions.
func Approve(pb *Playbook, p Proposal, all []Artifact) ([]Change, error) {
	if len(p.Items) == 0 {
		return nil, errors.New("a Proposal needs at least one item")
	}
	all = slices.Clone(all)
	refs := map[string]string{} // ref -> the id allocated to its creation
	resolve := func(id string) string {
		if r, ok := refs[id]; ok {
			return r
		}
		return id
	}
	var changes []Change
	for i, it := range p.Items {
		fail := func(err error) ([]Change, error) {
			return nil, fmt.Errorf("item %d (%s): %w", i+1, it, err)
		}
		switch {
		case it.Create != "" && it.Move == "" && it.To == "":
			if _, dup := refs[it.Ref]; dup {
				return fail(fmt.Errorf("ref %q is used by an earlier item", it.Ref))
			}
			links := map[string][]string{}
			for name, ids := range it.Links {
				for _, id := range ids {
					links[name] = append(links[name], resolve(id))
				}
			}
			if len(links) == 0 {
				links = nil
			}
			a, err := Create(pb, Actor{}, it.Create, it.Title, it.Status, nil, links, all)
			if err != nil {
				return fail(err)
			}
			if it.Ref != "" {
				refs[it.Ref] = a.ID
			}
			all = append(all, a)
			changes = append(changes, Change{Item: i, Artifact: a})
		case it.Move != "" && it.To != "" && it.Create == "":
			id := resolve(it.Move)
			j := slices.IndexFunc(all, func(o Artifact) bool { return o.ID == id })
			if j < 0 {
				return fail(fmt.Errorf("%s doesn't exist", id))
			}
			moved, tr, err := Move(pb, Actor{}, all[j], it.To, all)
			if err != nil {
				return fail(err)
			}
			changes = append(changes, Change{Item: i, Artifact: moved, From: all[j].Status, Transition: tr})
			all[j] = moved
		default:
			return nil, fmt.Errorf("item %d: an item either creates (create, title) or moves (move, to)", i+1)
		}
	}
	return changes, nil
}

// touched returns, for every Artifact an item of a pending Proposal moves, the
// id of that Proposal. Creations touch nothing yet: their Artifacts don't
// exist until approval.
func touched(proposals []Proposal) map[string]string {
	out := map[string]string{}
	for _, p := range proposals {
		if p.Status != Pending {
			continue
		}
		refs := map[string]bool{}
		for _, it := range p.Items {
			if it.Ref != "" {
				refs[it.Ref] = true
			}
			if it.Move != "" && !refs[it.Move] {
				if _, ok := out[it.Move]; !ok {
					out[it.Move] = p.ID
				}
			}
		}
	}
	return out
}
