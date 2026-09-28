package engine

import (
	"fmt"
	"slices"
	"strings"
)

// Orphaned is the error of a Playbook that would leave Artifacts outside
// its Status machine: each Artifact whose Status its Artifact Type doesn't
// declare (ADR 0010). Such a Playbook doesn't load. Unmapped says they are
// the ones no Playbook Migration maps, which Migrate refuses.
type Orphaned struct {
	Orphans  []Orphan // by Artifact id
	Unmapped bool
}

// Orphan is an Artifact in an undeclared Status, and the Status a Playbook
// Migration maps it to, if any.
type Orphan struct {
	Artifact Artifact
	To       string
}

func (e *Orphaned) Error() string {
	n := "1 Artifact"
	if len(e.Orphans) != 1 {
		n = fmt.Sprintf("%d Artifacts", len(e.Orphans))
	}
	head := fmt.Sprintf("the Playbook leaves %s in an undeclared Status", n)
	if e.Unmapped {
		head = "no Playbook Migration maps " + n
	}
	lines := make([]string, len(e.Orphans))
	for i, o := range e.Orphans {
		a := o.Artifact
		lines[i] = fmt.Sprintf("%s %q: %s has no Status %q", a.ID, a.Title, a.Type, a.Status)
		if o.To != "" {
			lines[i] += fmt.Sprintf(", which a Playbook Migration maps to %q", o.To)
		}
	}
	return head + ":\n  " + strings.Join(lines, "\n  ")
}

// Migrated is an Artifact a Playbook Migration moved, and the Status it
// moved from.
type Migrated struct {
	Artifact Artifact
	From     string
}

// CheckOrphans returns an *Orphaned error when any of all, every Artifact
// in the Store, is in a Status its Artifact Type doesn't declare, whether a
// Playbook Migration maps it or not: until it is migrated, it is outside
// the Status machine.
func CheckOrphans(pb *Playbook, all []Artifact) error {
	if orphans := orphans(pb, all); len(orphans) > 0 {
		return &Orphaned{Orphans: orphans}
	}
	return nil
}

// Migrate decides applying the Playbook's Migrations to all, every Artifact
// in the Store: each Artifact in a Status its Artifact Type no longer
// declares moves to the Status a Playbook Migration maps it to. It returns
// the migrated Artifacts by id; the caller saves them. As with a
// Transition, migrating into a Status that isn't agent work releases the
// Claim.
//
// A migration is all or nothing: while any such Artifact has no mapping, it
// migrates none and returns an *Orphaned error listing the unmapped ones.
func Migrate(pb *Playbook, all []Artifact) ([]Migrated, error) {
	var out []Migrated
	unmapped := &Orphaned{Unmapped: true}
	for _, o := range orphans(pb, all) {
		if o.To == "" {
			unmapped.Orphans = append(unmapped.Orphans, o)
			continue
		}
		a := o.Artifact
		a.Status = o.To
		if !pb.AgentWork(a) {
			a.Claim = ""
		}
		out = append(out, Migrated{Artifact: a, From: o.Artifact.Status})
	}
	if len(unmapped.Orphans) > 0 {
		return nil, unmapped
	}
	return out, nil
}

// orphans returns, by id, the Artifacts in all whose Status their Artifact
// Type doesn't declare.
func orphans(pb *Playbook, all []Artifact) []Orphan {
	var out []Orphan
	for _, a := range all {
		if t := pb.Type(a.Type); t != nil && !slices.Contains(t.Statuses, a.Status) {
			out = append(out, Orphan{Artifact: a, To: t.Migrations[a.Status]})
		}
	}
	slices.SortFunc(out, func(a, b Orphan) int { return strings.Compare(a.Artifact.ID, b.Artifact.ID) })
	return out
}
