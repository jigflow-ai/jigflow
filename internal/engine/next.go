package engine

import (
	"cmp"
	"fmt"
	"slices"
)

// Candidate is an Artifact an agent could work on, with the Skill its
// Status is bound to.
type Candidate struct {
	Artifact Artifact
	Skill    string
}

// Skip is a non-final Artifact that `next` didn't offer, and why.
type Skip struct {
	Artifact Artifact
	Reason   string
}

// NextResult is what `next` found: the candidates in the order they should
// be picked (the first is the pick), and the Artifacts it skipped.
type NextResult struct {
	Candidates []Candidate
	Skipped    []Skip
}

// Next computes which Skill to run on which Artifact. Artifacts are
// considered in declaration order: by Artifact Type in Playbook order, then
// by the number in their id. Final Artifacts are left out silently; an
// Artifact whose Status has no Binding is skipped as human work, one that an
// item of a pending Proposal moves is skipped until a person decides on it,
// and one whose Status's Readiness fails is skipped as not ready. Guards play
// no part: they are checked only at the moment of a Transition.
func Next(pb *Playbook, artifacts []Artifact, proposals []Proposal) NextResult {
	var res NextResult
	pending := touched(proposals)
	for _, a := range declarationOrder(pb, artifacts) {
		t := pb.Type(a.Type)
		if t == nil {
			res.Skipped = append(res.Skipped, Skip{a, fmt.Sprintf("Artifact Type %q isn't declared in the Playbook", a.Type)})
			continue
		}
		if slices.Contains(t.Final, a.Status) {
			continue
		}
		skill := t.Bindings[a.Status]
		if skill == "" {
			res.Skipped = append(res.Skipped, Skip{a, fmt.Sprintf("%q has no Binding, so it's human work", a.Status)})
			continue
		}
		if pid, ok := pending[a.ID]; ok {
			res.Skipped = append(res.Skipped, Skip{a, fmt.Sprintf("waiting on a pending Proposal (%s)", pid)})
			continue
		}
		if f := failed(t.Readiness[a.Status], a, artifacts); len(f) > 0 {
			res.Skipped = append(res.Skipped, Skip{a, "not ready: waiting until " + describe(f)})
			continue
		}
		res.Candidates = append(res.Candidates, Candidate{a, skill})
	}
	return res
}

func declarationOrder(pb *Playbook, artifacts []Artifact) []Artifact {
	typeRank := func(name string) int {
		i := slices.IndexFunc(pb.Types, func(t *ArtifactType) bool { return t.Name == name })
		if i < 0 {
			return len(pb.Types)
		}
		return i
	}
	number := func(a Artifact) int {
		if t := pb.Type(a.Type); t != nil {
			if n, ok := idNumber(t.Prefix, a.ID); ok {
				return n
			}
		}
		return 0
	}
	sorted := slices.Clone(artifacts)
	slices.SortStableFunc(sorted, func(x, y Artifact) int {
		return cmp.Or(
			cmp.Compare(typeRank(x.Type), typeRank(y.Type)),
			cmp.Compare(number(x), number(y)),
			cmp.Compare(x.ID, y.ID),
		)
	})
	return sorted
}
