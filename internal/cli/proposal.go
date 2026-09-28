package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

func cmdPropose(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl propose <file>", errUsage)
	}
	path := args[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	summary, items, err := store.ParseProposal(data)
	if err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	pb, st, err := e.load()
	if err != nil {
		return err
	}
	all, err := st.List()
	if err != nil {
		return err
	}
	ps := store.NewProposals(e.dir)
	existing, err := ps.List()
	if err != nil {
		return err
	}
	p, err := engine.Propose(pb, e.actor, summary, items, all, existing)
	if err != nil {
		return fmt.Errorf("not proposed: %w", err)
	}
	if err := ps.Save(p); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "proposed %s: %s (%s, waiting for a human)\n%s", p.ID, p.Summary, plural(len(p.Items), "change"), listItems(p))
	return nil
}

// listItems lists a Proposal's items, one numbered line each.
func listItems(p engine.Proposal) string {
	s := ""
	for i, it := range p.Items {
		s += fmt.Sprintf("  %d. %s\n", i+1, it)
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func cmdApprove(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl approve <proposal>", errUsage)
	}
	return e.approve(args[0], nil)
}

// approve applies every change of the pending Proposal id, or none. A
// person's edits to its items, made in the Dashboard, are applied to it
// first when edit isn't nil; the Proposal is kept as approved, with them.
func (e *env) approve(id string, edit func(pb *engine.Playbook, p *engine.Proposal) error) error {
	if e.actor.Agent() {
		return fmt.Errorf("Only a human can approve %s.", id)
	}
	pb, st, err := e.load()
	if err != nil {
		return err
	}
	ps := store.NewProposals(e.dir)
	p, err := ps.Get(id)
	if err != nil {
		return err
	}
	if p.Status != engine.Pending {
		return fmt.Errorf("%s isn't pending: it was %s", p.ID, p.Status)
	}
	if edit != nil {
		before := slices.Clone(p.Items)
		if err := edit(pb, &p); err != nil {
			return fmt.Errorf("%s: not approved: %w", p.ID, err)
		}
		for i, it := range p.Items {
			if it.String() != before[i].String() {
				fmt.Fprintf(e.stdout, "item %d edited: %s\n", i+1, it)
			}
		}
	}
	notApplied := func(err error) error {
		return fmt.Errorf("%s was not applied at all (all or nothing): %w", p.ID, err)
	}
	all, err := st.List()
	if err != nil {
		return err
	}
	changes, err := engine.Approve(pb, p, all)
	if err != nil {
		return notApplied(err)
	}
	// Re-validate edits made outside the CLI to every existing Artifact the
	// Proposal moves, as move does (ADR 0002).
	created := map[string]bool{}
	for _, c := range changes {
		if c.Created() {
			created[c.Artifact.ID] = true
		}
	}
	verify := func() error {
		for _, c := range changes {
			if created[c.Artifact.ID] {
				continue
			}
			bodyEdited, err := st.Verify(c.Artifact.ID)
			if err != nil {
				return notApplied(fmt.Errorf("item %d (%s): %w", c.Item+1, p.Items[c.Item], err))
			}
			if bodyEdited {
				fmt.Fprintf(e.stdout, "%s: body edited outside jfl, re-validated\n", c.Artifact.ID)
			}
		}
		return nil
	}
	if err := verify(); err != nil {
		return err
	}

	// A Transition the Playbook requires the Dashboard for is approved
	// there only, or a Proposal would be a way around it.
	for _, c := range changes {
		if c.Transition.Dashboard && !e.clicked {
			return notApplied(fmt.Errorf("item %d (%s): %s and approve %s there", c.Item+1, p.Items[c.Item], dashboardOnly(c.Artifact.ID, c.Transition), p.ID))
		}
	}
	if err := e.confirmApproval(p); err != nil {
		return err
	}

	// Every Gate of every Transition must pass before anything is saved.
	for _, c := range changes {
		for _, g := range c.Transition.Gates {
			if g.Cmd == "" {
				return notApplied(fmt.Errorf("item %d (%s): %s", c.Item+1, p.Items[c.Item], noCommand(g)))
			}
			if out, err := e.shell(g.Cmd, c.Artifact.ID, c.From, c.Artifact.Status); err != nil {
				return notApplied(fmt.Errorf("item %d (%s): Gate %q failed (%s: %v)%s", c.Item+1, p.Items[c.Item], g.Name, g.Cmd, err, indent(out)))
			}
		}
	}
	if err := verify(); err != nil {
		return err
	}
	// The Playbook's changes are written first, all of them or none, so
	// that a Playbook they'd break changes no Artifact either.
	var playbookItems []engine.ProposalItem
	for _, it := range p.Items {
		if it.ChangesPlaybook() {
			playbookItems = append(playbookItems, it)
		}
	}
	if len(playbookItems) > 0 {
		if err := playbook.Apply(e.dir, playbookItems); err != nil {
			return notApplied(err)
		}
	}
	// A tracker gives each Artifact it creates its own id, so the ids the
	// engine allocated are renamed, in the Links and moves of later items
	// too. A creation an agent proposed is its text, so it is created as
	// that agent's, carrying the AI-generated marker.
	renamed := map[string]string{}
	rename := func(id string) string {
		if r, ok := renamed[id]; ok {
			return r
		}
		return id
	}
	for i := range changes {
		c := &changes[i]
		c.Artifact.ID = rename(c.Artifact.ID)
		for name, ids := range c.Artifact.Links {
			for j, id := range ids {
				c.Artifact.Links[name][j] = rename(id)
			}
		}
		if c.Created() {
			kept, err := st.Create(c.Artifact, engine.Actor{Session: p.By})
			if err != nil {
				return err
			}
			renamed[c.Artifact.ID] = kept.ID
			c.Artifact = kept
		} else if err := st.Save(c.Artifact); err != nil {
			return err
		}
		if err := e.recordStatus(c.Artifact, c.From); err != nil {
			return err
		}
		if err := e.unfocus(pb, c.Artifact); err != nil {
			return err
		}
	}
	p.Status = engine.Approved
	if err := ps.Save(p); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "approved %s as one unit:\n", p.ID)
	for _, it := range playbookItems {
		fmt.Fprintf(e.stdout, "  %s\n", it)
	}
	for _, c := range changes {
		if c.Created() {
			fmt.Fprintf(e.stdout, "  created %s %q in %s\n", c.Artifact.ID, c.Artifact.Title, c.Artifact.Status)
		} else {
			fmt.Fprintf(e.stdout, "  %s: %s → %s\n", c.Artifact.ID, c.From, c.Artifact.Status)
		}
	}
	// The Proposal has been applied; a failing Action is reported, stops the
	// Actions after it, and makes the command fail, but undoes nothing.
	for _, c := range changes {
		for _, act := range c.Transition.Actions {
			out, err := e.shell(act.Cmd, c.Artifact.ID, c.From, c.Artifact.Status)
			if err != nil {
				return fmt.Errorf("%s: approved, but Action %q of %s failed (%s: %v)%s", p.ID, act.Name, c.Artifact.ID, act.Cmd, err, indent(out))
			}
			fmt.Fprintf(e.stdout, "Action %q succeeded%s\n", act.Name, indent(out))
		}
	}
	return nil
}

func cmdReject(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl reject <proposal>", errUsage)
	}
	return e.reject(args[0])
}

// reject drops the pending Proposal id, changing nothing.
func (e *env) reject(id string) error {
	if e.actor.Agent() {
		return fmt.Errorf("Only a human can reject %s.", id)
	}
	// Rejecting changes no Artifact, but no command runs on an invalid
	// Playbook.
	if _, _, err := e.load(); err != nil {
		return err
	}
	ps := store.NewProposals(e.dir)
	p, err := ps.Get(id)
	if err != nil {
		return err
	}
	if p.Status != engine.Pending {
		return fmt.Errorf("%s isn't pending: it was %s", p.ID, p.Status)
	}
	p.Status = engine.Rejected
	if err := ps.Save(p); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "rejected %s. Nothing changed.\n", p.ID)
	return nil
}
