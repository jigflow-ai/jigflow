package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// noLongerSeen returns the Artifacts that the Store of each Connector the
// items change lists as the Playbook pb is and doesn't list as the items
// would make it, next. A change to a Connector's settings may re-point its
// Store, as GitHub's repo or Linear's team does, which is allowed but never
// silently (ADR 0030); since any setting may, it is told only by listing
// the Store both ways. An Artifact is still seen when the Store lists one
// with its id and title after: another repository numbers its own items as
// this one does. A Store that can't be listed is a tracker problem.
//
// A Connector whose command or args the items change isn't listed as they
// would make it, since that runs a command no person has approved yet.
func (e *env) noLongerSeen(pb, next *engine.Playbook, items []engine.ProposalItem) ([]engine.Artifact, error) {
	var lost []engine.Artifact
	var names []string
	for _, it := range items {
		if it.Connector != "" && !slices.Contains(names, it.Connector) {
			names = append(names, it.Connector)
		}
	}
	for _, name := range names {
		now, then := pb.Connectors[name], next.Connectors[name]
		if now == nil || then == nil || then.Command != now.Command || !slices.Equal(then.Args, now.Args) {
			continue
		}
		before, err := store.NewConnector(e.dir, pb, now).List()
		if err != nil {
			return nil, err
		}
		after, err := store.NewConnector(e.dir, next, then).List()
		if err != nil {
			return nil, err
		}
		seen := map[[2]string]bool{}
		for _, a := range after {
			seen[[2]string{a.ID, a.Title}] = true
		}
		for _, a := range before {
			if !seen[[2]string{a.ID, a.Title}] {
				lost = append(lost, a)
			}
		}
	}
	return engine.InOrder(pb, lost), nil
}

// unseenBy returns the Artifacts that a Store the pending Proposal p
// re-points will no longer see, as noLongerSeen does.
func (e *env) unseenBy(pb *engine.Playbook, p engine.Proposal) ([]engine.Artifact, error) {
	changes := playbookItems(p.Items)
	if !slices.ContainsFunc(changes, func(it engine.ProposalItem) bool { return it.Connector != "" }) {
		return nil, nil
	}
	next, err := e.candidate(changes)
	if err != nil {
		return nil, err
	}
	return e.noLongerSeen(pb, next, changes)
}

// trouble is what a person reads of err: a tracker problem said to be one,
// not a workflow refusal.
func trouble(err error) string {
	if _, ok := errors.AsType[*store.ConnectorError](err); ok {
		return "tracker problem, not a workflow refusal: " + err.Error()
	}
	return err.Error()
}

// unseenLine says how many Artifacts, and which, will no longer be seen, or
// nothing when there are none.
func unseenLine(lost []engine.Artifact) string {
	if len(lost) == 0 {
		return ""
	}
	return fmt.Sprintf("  %s will no longer be seen: %s\n", plural(len(lost), "Artifact"), strings.Join(ids(lost), ", "))
}

// ids are the ids of the Artifacts, in their order.
func ids(as []engine.Artifact) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}
