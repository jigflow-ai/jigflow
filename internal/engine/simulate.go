package engine

import "slices"

// Path is one way a pretend Artifact can go through its Artifact Type: from
// an initial Status to a final one, visiting no Status twice.
type Path struct {
	Statuses    []string     // in the order they are visited
	Transitions []Transition // Transitions[i] leads from Statuses[i] to Statuses[i+1]
}

// Paths returns every Path through t, in declaration order: by initial
// Status, then by the order of the Transitions out of each Status. A Path
// ends at the first final Status it reaches, and never takes a Transition
// back to a Status it has visited, so the loops of a Status machine (a
// review sending work back, say) don't make it endless.
func Paths(t *ArtifactType) []Path {
	var paths []Path
	var walk func(statuses []string, trs []Transition)
	walk = func(statuses []string, trs []Transition) {
		at := statuses[len(statuses)-1]
		if slices.Contains(t.Final, at) {
			paths = append(paths, Path{Statuses: slices.Clone(statuses), Transitions: slices.Clone(trs)})
			return
		}
		for _, tr := range t.Transitions {
			if tr.From == at && !slices.Contains(statuses, tr.To) {
				walk(append(statuses, tr.To), append(trs, tr))
			}
		}
	}
	for _, s := range t.Initial {
		walk([]string{s}, nil)
	}
	return paths
}
