package cli

import (
	"cmp"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// artifactPage is one Artifact on a page of its own.
type artifactPage struct {
	chrome
	ID, Type, Title, Status string
	Fields                  []field
	Work                    engine.Work
	Note                    string // why next doesn't hand out agent work, if it doesn't
	Claim                   string
	Moves                   []string // the Statuses its Human Transitions lead to
	Outcome                 *outcome // what the person's comment did, if they just posted one
	Draft                   string   // the comment they posted, kept when it was refused
	CanAct                  bool     // whether the person viewing it may decide here
	Cannot                  string   // why not, when they may not
	Proposals               []pendingProposal
	Links                   []outgoingLink  // the Links it declares, by name
	LinkedHere              []incomingLinks // the Links of other Artifacts to it
	History                 []historyEntry
	Agent                   time.Duration // agent session time charged to it
	AsOf                    time.Time     // when times so far are summed to
	Git                     bool          // whether the project is in a git repository
	Commits                 []gitCommit   // those whose subject starts with its id, newest first
	Body                    template.HTML // rendered from Markdown
	Mockups                 []mockupFile  // those its body links to, in the Playbook's Mockup folder or missing from it
	// Comments are those a tracker keeps apart from the body, rendered,
	// oldest first; a file keeps its comments in its body.
	Comments []template.HTML
}

// field is one field of an Artifact and its value.
type field struct {
	Name, Value string
}

// outgoingLink is a named Link of the Artifact and the Artifacts it points
// to.
type outgoingLink struct {
	Name      string
	Artifacts []linkedArtifact
}

// incomingLinks is the Artifacts of one Type linking to the Artifact by
// one Link name, and how many of them are finished.
type incomingLinks struct {
	Type, Name string
	Artifacts  []linkedArtifact
	Done       int
}

// linkedArtifact is a linked Artifact as a page lists it; with only its id
// when no Store keeps it.
type linkedArtifact struct {
	ID, Title, Status string
}

// historyEntry is a Status change of the Artifact, from the Ledger, and
// how long it stayed in the Status it entered, as jfl ledger sums it: until
// its next change, or so far while it is still there, and none in a final
// Status.
type historyEntry struct {
	engine.StatusChange
	Time time.Duration
	Now  bool // still there
}

// artifactView builds the page of the Artifact id as the person making the
// request sees it, with the outcome of the comment they just posted, if any.
func (d *dashboard) artifactView(r *http.Request, id string, done *outcome) func() (any, error) {
	return func() (any, error) {
		v, err := d.e.artifact(id)
		if err != nil && done != nil {
			// The comment was added, or refused, all the same.
			said := strings.TrimSpace(done.Problem + "\n" + done.Said)
			return nil, fmt.Errorf("%s\n\nThe page of %s can't be shown now: %w", said, id, err)
		}
		if err != nil {
			return nil, err
		}
		v.Outcome = done
		if done != nil && done.Refused {
			v.Draft = r.PostForm.Get("text")
		}
		if err := d.mayAct(r); err != nil {
			v.Cannot = err.Error()
		} else {
			v.CanAct = true
		}
		return v, nil
	}
}

// artifact reads the Artifact id, what links to it, the Proposals touching
// it and its Ledger history afresh, as its page shows them.
func (e *env) artifact(id string) (artifactPage, error) {
	pb, st, err := e.load()
	if err != nil {
		return artifactPage{}, err
	}
	a, err := st.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		return artifactPage{}, fmt.Errorf("there is no Artifact %s: %w", id, err)
	}
	if err != nil {
		return artifactPage{}, err
	}
	all, err := st.List()
	if err != nil {
		return artifactPage{}, err
	}
	proposals, err := store.NewProposals(e.dir).List()
	if err != nil {
		return artifactPage{}, err
	}
	// Pages are served concurrently, so each reads the Ledger through its
	// own handle rather than the command's.
	l, err := store.NewLedger(e.dir).Read()
	if err != nil {
		return artifactPage{}, err
	}
	text, err := st.Text(a.ID)
	if err != nil {
		return artifactPage{}, err
	}

	v := artifactPage{
		chrome: chrome{Playbook: pb.Name, Page: "artifact", Path: "/artifacts/" + url.PathEscape(a.ID)},
		ID:     a.ID, Type: a.Type, Title: a.Title, Status: a.Status,
		Work: pb.WorkOf(a), Claim: a.Claim,
		AsOf: e.now(),
	}
	for _, name := range slices.Sorted(maps.Keys(a.Fields)) {
		v.Fields = append(v.Fields, field{name, a.Fields[name]})
	}
	if t := pb.Type(a.Type); t != nil {
		v.Moves = humanMoves(t, a)
	}
	// Why next skips it, as a person sees it: a Claim is shown as such.
	if v.Work == engine.ForAgent && a.Claim == "" {
		for _, s := range engine.Next(pb, engine.Actor{}, all, proposals).Skipped {
			if s.Artifact.ID == a.ID {
				v.Note = s.Reason
			}
		}
	}
	for _, p := range proposals {
		if p.Status == engine.Pending && touches(p, a.ID) {
			v.Proposals = append(v.Proposals, pendingProposal{ID: p.ID, By: p.ProposedBy(), Summary: p.Summary})
		}
	}
	v.Links = linksFrom(a, byID(all))
	v.LinkedHere = linksTo(pb, a, all)
	v.History, v.Agent = history(pb, l, a, v.AsOf)
	if _, v.Git = gitDir(e.dir); v.Git {
		if v.Commits, err = commitsOf(e.dir, a.ID); err != nil {
			return artifactPage{}, err
		}
	}
	var linked []string
	if v.Body, linked, err = renderLinkingMockups(text.Body, pb.Mockups); err != nil {
		return artifactPage{}, err
	}
	if v.Mockups, err = e.linkedMockups(pb.Mockups, linked); err != nil {
		return artifactPage{}, err
	}
	for _, c := range text.Comments {
		r, err := renderMarkdown(c)
		if err != nil {
			return artifactPage{}, err
		}
		v.Comments = append(v.Comments, r)
	}
	return v, nil
}

// touches reports whether the Proposal p moves the Artifact id, or creates
// an Artifact linked to it.
func touches(p engine.Proposal, id string) bool {
	return slices.ContainsFunc(p.Items, func(it engine.ProposalItem) bool {
		return slices.Contains(it.Names(), id)
	})
}

// linksFrom returns the Links of a, by name, each with the Artifacts it
// points to as kept.
func linksFrom(a engine.Artifact, kept map[string]engine.Artifact) []outgoingLink {
	var out []outgoingLink
	for _, name := range slices.Sorted(maps.Keys(a.Links)) {
		l := outgoingLink{Name: name}
		for _, id := range a.Links[name] {
			o := kept[id]
			l.Artifacts = append(l.Artifacts, linkedArtifact{id, o.Title, o.Status})
		}
		out = append(out, l)
	}
	return out
}

// linksTo returns the Artifacts of all linking to a, grouped by their Type
// and the Link's name.
func linksTo(pb *engine.Playbook, a engine.Artifact, all []engine.Artifact) []incomingLinks {
	groups := map[[2]string]*incomingLinks{}
	for _, o := range engine.InOrder(pb, all) {
		for name, ids := range o.Links {
			if !slices.Contains(ids, a.ID) {
				continue
			}
			k := [2]string{o.Type, name}
			g := groups[k]
			if g == nil {
				g = &incomingLinks{Type: o.Type, Name: name}
				groups[k] = g
			}
			g.Artifacts = append(g.Artifacts, linkedArtifact{o.ID, o.Title, o.Status})
			if pb.WorkOf(o) == engine.Finished {
				g.Done++
			}
		}
	}
	var in []incomingLinks
	for _, k := range slices.SortedFunc(maps.Keys(groups), func(x, y [2]string) int {
		return cmp.Or(cmp.Compare(x[0], y[0]), cmp.Compare(x[1], y[1]))
	}) {
		in = append(in, *groups[k])
	}
	return in
}

// history returns the Status changes of the Artifact a in the Ledger l,
// each with the time it stayed in the Status it entered, and the agent
// session time charged to it, as jfl ledger sums them at now.
func history(pb *engine.Playbook, l engine.Ledger, a engine.Artifact, now time.Time) ([]historyEntry, time.Duration) {
	var agent time.Duration
	for _, at := range engine.Summarise(pb, l, now).Artifacts {
		if at.ID == a.ID {
			agent = at.Agent
		}
	}
	return statusHistory(pb, l, a.ID, a.Type, now), agent
}

// statusHistory returns the Status changes in the Ledger l of the Artifact
// id, of the Artifact Type typ, each with the time it stayed in the Status
// it entered: until its next change, or so far at now while it is still
// there, and none in a final Status.
func statusHistory(pb *engine.Playbook, l engine.Ledger, id, typ string, now time.Time) []historyEntry {
	var h []historyEntry
	for _, c := range l.Statuses {
		if c.Artifact != id {
			continue
		}
		if n := len(h); n > 0 {
			h[n-1].Time = max(c.At.Sub(h[n-1].At), 0)
		}
		h = append(h, historyEntry{StatusChange: c})
	}
	// No work waits in a final Status, so no time is summed there.
	if n := len(h); n > 0 {
		if t := pb.Type(typ); t == nil || !slices.Contains(t.Final, h[n-1].To) {
			h[n-1].Time, h[n-1].Now = max(now.Sub(h[n-1].At), 0), true
		}
	}
	return h
}
