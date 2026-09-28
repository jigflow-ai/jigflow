package engine

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
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

// ProposalItem is one change in a Proposal: the creation of an Artifact of
// the Type Create, with values for its fields, the Transition of the Artifact Move to the Status To, or
// a change to the Playbook (ADR 0004): giving the Gates named Gate the
// command Cmd, or adding the Guideline named Guideline, whose Markdown is
// Text.
//
// A creation's id is allocated only on approval, so other items of the same
// Proposal refer to it by its Ref, in their Links or as the Artifact they
// move.
type ProposalItem struct {
	Create string
	Ref    string
	Title  string
	Status string              // empty means the Type's first initial Status
	Fields map[string]string   // field -> its value
	Links  map[string][]string // Link name -> ids or refs
	Move   string              // an id or a ref
	To     string

	Gate      string
	Cmd       string
	Guideline string
	Text      string
}

// ChangesPlaybook reports whether the item changes the Playbook rather
// than an Artifact.
func (it ProposalItem) ChangesPlaybook() bool { return it.Gate != "" || it.Guideline != "" }

// String describes the item for a person deciding on it.
func (it ProposalItem) String() string {
	if it.Create != "" {
		s := fmt.Sprintf("create %s %q", it.Create, it.Title)
		if it.Status != "" {
			s += " in " + it.Status
		}
		var fields []string
		for _, name := range slices.Sorted(maps.Keys(it.Fields)) {
			fields = append(fields, name+" "+it.Fields[name])
		}
		if len(fields) > 0 {
			s += " (" + strings.Join(fields, ", ") + ")"
		}
		for _, name := range slices.Sorted(maps.Keys(it.Links)) {
			s += fmt.Sprintf(", %s %s", name, strings.Join(it.Links[name], ", "))
		}
		return s
	}
	switch {
	case it.Gate != "":
		return fmt.Sprintf("give Gate %q the command %s", it.Gate, it.Cmd)
	case it.Guideline != "":
		return fmt.Sprintf("add Guideline %q (%s)", it.Guideline, plural(strings.Count(strings.TrimRight(it.Text, "\n"), "\n")+1, "line"))
	}
	return fmt.Sprintf("move %s → %s", it.Move, it.To)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// guidelineName is what a Guideline's name may be: Skills name it, and it
// is the name of its file.
var guidelineName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
// Items that change the Playbook are checked but make no Change: the caller
// writes them into the Playbook.
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
		kinds := 0
		for _, is := range []bool{it.Create != "", it.Move != "" || it.To != "", it.Gate != "" || it.Cmd != "", it.Guideline != "" || it.Text != ""} {
			if is {
				kinds++
			}
		}
		switch {
		case kinds != 1:
			return nil, fmt.Errorf("item %d: %s", i+1, itemKinds)
		case it.Gate != "" || it.Cmd != "":
			if strings.TrimSpace(it.Gate) == "" || strings.TrimSpace(it.Cmd) == "" {
				return fail(errors.New("giving a Gate its command needs the Gate's name and a cmd"))
			}
		case it.Guideline != "" || it.Text != "":
			if !guidelineName.MatchString(it.Guideline) {
				return fail(fmt.Errorf("a Guideline's name is its file's, which Skills refer to: letters, digits, '.', '_' and '-', such as conventions; not %q", it.Guideline))
			}
			if strings.TrimSpace(it.Text) == "" {
				return fail(errors.New("a Guideline needs its Markdown, as text"))
			}
		case it.Create != "":
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
			a, err := Create(pb, Actor{}, it.Create, it.Title, it.Status, it.Fields, links, all)
			if err != nil {
				return fail(err)
			}
			if it.Ref != "" {
				refs[it.Ref] = a.ID
			}
			all = append(all, a)
			changes = append(changes, Change{Item: i, Artifact: a})
		case it.Move != "" && it.To != "":
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
			return nil, fmt.Errorf("item %d: %s", i+1, itemKinds)
		}
	}
	return changes, nil
}

// itemKinds says what a Proposal item may be.
const itemKinds = "an item either creates (create, title, and optionally status, fields and links), moves (move, to), gives a Gate its command (gate, cmd) or adds a Guideline (guideline, text)"

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
