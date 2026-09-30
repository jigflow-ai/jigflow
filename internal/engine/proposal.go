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
// command Cmd, or, with Remove, removing the command the project's
// Playbook file gives them, so that they run the Base Playbook's (ADR
// 0030), setting the Mockup folder to Mockups, or, with Remove, removing
// the folder Mockups the project's Playbook file gives, so that the Base
// Playbook's is the folder again, adding the Guideline named Guideline, whose Markdown is Text,
// declaring the Artifact Type named Type, whose YAML file is Text, or writing
// the Skill named Skill, whose SKILL.md is Text. A Type or a Skill the
// Playbook has already is replaced, so the Playbook's own Types and Skills,
// and those of its Base Playbook, which the project's override by name, can
// be changed as well as added.
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
	Remove    bool // removes the value the item names from the project's Playbook file
	Mockups   string
	Guideline string
	Type      string
	Skill     string
	Text      string
}

// Names returns the ids and refs the item names: the Artifact it moves and
// those its Links point to, each once, sorted.
func (it ProposalItem) Names() []string {
	var names []string
	if it.Move != "" {
		names = append(names, it.Move)
	}
	for _, ids := range it.Links {
		names = append(names, ids...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// ProposedBy says who put the Proposal forward: an agent session, or a
// person.
func (p Proposal) ProposedBy() string {
	if p.By != "" {
		return "agent session " + p.By
	}
	return "a person"
}

// ChangesPlaybook reports whether the item changes the Playbook rather
// than an Artifact.
func (it ProposalItem) ChangesPlaybook() bool {
	return it.Gate != "" || it.Mockups != "" || it.Guideline != "" || it.Type != "" || it.Skill != ""
}

// FileValue names the value of the Playbook file the item changes, as the
// path of keys it is kept under, such as gates.tests, or is empty when it
// changes none: an Artifact, or a Playbook file of its own such as a
// Guideline's. Each kind of item that changes a value of the Playbook file
// names it here, and Playbook.Now says what that value is (ADR 0030).
func (it ProposalItem) FileValue() string {
	switch {
	case it.Gate != "":
		return "gates." + it.Gate
	case it.Mockups != "":
		return "mockups"
	}
	return ""
}

// Now says what the value of the Playbook file that the item changes is
// in pb as things stand, which approving the item replaces, as a person
// reads it: empty when it has none yet. It reports false when the item
// changes no value of the Playbook file.
func (pb *Playbook) Now(it ProposalItem) (string, bool) {
	switch {
	case it.Gate != "":
		// The command the Gates of that name run, which a Playbook file
		// gives them over the one they are declared with.
		if cmd, ok := pb.Gates[it.Gate]; ok {
			return cmd, true
		}
		for _, t := range pb.Types {
			for _, tr := range t.Transitions {
				for _, g := range tr.Gates {
					if g.Name == it.Gate && g.Cmd != "" {
						return g.Cmd, true
					}
				}
			}
		}
		return "", true
	case it.Mockups != "":
		return pb.Mockups, true
	}
	return "", false
}

// Changing returns, for each value of the Playbook file that a pending
// Proposal changes, by the path FileValue names it by, the ids of those
// Proposals, each once, in the order of proposals.
func Changing(proposals []Proposal) map[string][]string {
	out := map[string][]string{}
	for _, p := range proposals {
		if p.Status != Pending {
			continue
		}
		for _, it := range p.Items {
			if v := it.FileValue(); v != "" && !slices.Contains(out[v], p.ID) {
				out[v] = append(out[v], p.ID)
			}
		}
	}
	return out
}

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
	case it.Gate != "" && it.Remove:
		return fmt.Sprintf("remove the project's command for Gate %q", it.Gate)
	case it.Gate != "":
		return fmt.Sprintf("give Gate %q the command %s", it.Gate, it.Cmd)
	case it.Mockups != "" && it.Remove:
		return "remove the project's Mockup folder " + it.Mockups
	case it.Mockups != "":
		return "set the Mockup folder to " + it.Mockups
	case it.Guideline != "":
		return fmt.Sprintf("add Guideline %q (%s)", it.Guideline, it.lines())
	case it.Type != "":
		return fmt.Sprintf("declare Artifact Type %q (%s)", it.Type, it.lines())
	case it.Skill != "":
		return fmt.Sprintf("write Skill %q (%s)", it.Skill, it.lines())
	}
	return fmt.Sprintf("move %s → %s", it.Move, it.To)
}

// lines says how long the item's text is.
func (it ProposalItem) lines() string {
	return plural(strings.Count(strings.TrimRight(it.Text, "\n"), "\n")+1, "line")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// fileName is what a Guideline's or a Skill's name may be: Skills and
// Bindings name it, and it is the name of its file or directory.
var fileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
		for _, is := range []bool{it.Create != "", it.Move != "" || it.To != "", it.Gate != "" || it.Cmd != "", it.Mockups != "", it.Guideline != "", it.Type != "", it.Skill != ""} {
			if is {
				kinds++
			}
		}
		textual := it.Guideline != "" || it.Type != "" || it.Skill != ""
		switch {
		case kinds != 1 || (it.Text != "" && !textual) || (it.Remove && it.Gate == "" && it.Mockups == ""):
			return nil, fmt.Errorf("item %d: %s", i+1, itemKinds)
		case it.Mockups != "":
			// The folder's place is checked with the Playbook it makes.
		case it.Remove:
			if it.Cmd != "" {
				return fail(errors.New("removing a Gate's command from the Playbook file takes no cmd"))
			}
		case it.Gate != "" || it.Cmd != "":
			if strings.TrimSpace(it.Gate) == "" || strings.TrimSpace(it.Cmd) == "" {
				return fail(errors.New("giving a Gate its command needs the Gate's name and a cmd"))
			}
		case it.Guideline != "":
			if !fileName.MatchString(it.Guideline) {
				return fail(fmt.Errorf("a Guideline's name is its file's, which Skills refer to: letters, digits, '.', '_' and '-', such as conventions; not %q", it.Guideline))
			}
			if strings.TrimSpace(it.Text) == "" {
				return fail(errors.New("a Guideline needs its Markdown, as text"))
			}
		case it.Type != "":
			if strings.ContainsAny(it.Type, `/\`) || strings.HasPrefix(it.Type, ".") {
				return fail(fmt.Errorf("an Artifact Type's file is named after it, so its name can't start with '.' or hold '/' or '\\': not %q", it.Type))
			}
			if strings.TrimSpace(it.Text) == "" {
				return fail(errors.New("an Artifact Type needs its file's YAML, as text"))
			}
		case it.Skill != "":
			if !fileName.MatchString(it.Skill) {
				return fail(fmt.Errorf("a Skill's name is its directory's, which Bindings refer to: letters, digits, '.', '_' and '-', such as implement; not %q", it.Skill))
			}
			if strings.TrimSpace(it.Text) == "" {
				return fail(errors.New("a Skill needs its SKILL.md, as text"))
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
const itemKinds = "an item either creates (create, title, and optionally status, fields and links), moves (move, to), gives a Gate its command (gate, cmd) or removes the one the project's Playbook file gives it (gate, remove: true), sets the Mockup folder (mockups) or removes the one the project's Playbook file gives (mockups, remove: true), adds a Guideline (guideline, text), declares an Artifact Type (type, text) or writes a Skill (skill, text)"

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
