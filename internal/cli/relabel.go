package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// remaps returns each Status and field value that the items map to another
// label or state, in the Store of each Connector they change, with the
// Artifacts carrying the one it replaces as the Playbook pb is. Without
// relabelling them, each would read as another Status, or as its Type's
// first initial Status when it matches none, so approving the items
// relabels them (ADR 0030). A Store that can't be listed is a tracker
// problem.
func (e *env) remaps(pb, next *engine.Playbook, items []engine.ProposalItem) ([]store.Remap, error) {
	var out []store.Remap
	var names []string
	for _, it := range items {
		if it.Connector != "" && !slices.Contains(names, it.Connector) {
			names = append(names, it.Connector)
		}
	}
	for _, name := range names {
		now, then := pb.Connectors[name], next.Connectors[name]
		if now == nil || then == nil {
			continue
		}
		rs, err := store.NewConnector(e.dir, pb, now).Remaps(then)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	return out, nil
}

// remapsBy returns what the pending Proposal p remaps, as remaps does.
func (e *env) remapsBy(pb *engine.Playbook, p engine.Proposal) ([]store.Remap, error) {
	changes := playbookItems(p.Items)
	if !slices.ContainsFunc(changes, func(it engine.ProposalItem) bool { return it.Connector != "" }) {
		return nil, nil
	}
	next, err := e.candidate(changes)
	if err != nil {
		return nil, err
	}
	return e.remaps(pb, next, changes)
}

// relabel relabels, through each Connector of pb, the Artifacts carrying
// what the remaps replace. It changes all of them or none: when one
// Connector fails, those it relabelled are put back by it, and those the
// Connectors before it relabelled are put back here.
func (e *env) relabel(pb *engine.Playbook, remaps []store.Remap) error {
	var done [][]store.Remap
	for _, rs := range byConnector(remaps) {
		c := store.NewConnector(e.dir, pb, pb.Connectors[rs[0].Connector])
		err := c.Relabel(rs)
		if err == nil {
			done = append(done, rs)
			continue
		}
		for _, rs := range slices.Backward(done) {
			back := slices.Clone(rs)
			for i := range back {
				back[i].From, back[i].To = back[i].To, back[i].From
			}
			if undo := store.NewConnector(e.dir, pb, pb.Connectors[rs[0].Connector]).Relabel(back); undo != nil {
				err = errors.Join(err, fmt.Errorf("and the Artifacts of Connector %q couldn't all be put back: %w", rs[0].Connector, undo))
			}
		}
		return err
	}
	return nil
}

// byConnector groups the remaps by their Connector, in their order.
func byConnector(remaps []store.Remap) [][]store.Remap {
	var out [][]store.Remap
	for _, r := range remaps {
		if n := len(out); n > 0 && out[n-1][0].Connector == r.Connector {
			out[n-1] = append(out[n-1], r)
			continue
		}
		out = append(out, []store.Remap{r})
	}
	return out
}

// remapLines says, for each remapped Status or field value, how many
// Artifacts carry the label or state it replaces, and which, since
// approving relabels them.
func remapLines(remaps []store.Remap) string {
	s := ""
	for _, r := range remaps {
		s += "  " + remapLine(r) + "\n"
	}
	return s
}

// remapLine says what one remap relabels, and which Artifacts.
func remapLine(r store.Remap) string {
	if len(r.Carriers) == 0 {
		return remapSays(r)
	}
	return remapSays(r) + ": " + strings.Join(ids(r.Carriers), ", ")
}

// remapSays says what one remap relabels: how many Artifacts carry the
// label or state it replaces.
func remapSays(r store.Remap) string {
	what := fmt.Sprintf("%s from %s to %s: ", r.What(), r.From, r.To)
	switch len(r.Carriers) {
	case 0:
		return what + "no Artifact carries the old one, so none is relabelled"
	case 1:
		return what + "1 Artifact carries the old one and is relabelled"
	}
	return what + plural(len(r.Carriers), "Artifact") + " carry the old one and are relabelled"
}
