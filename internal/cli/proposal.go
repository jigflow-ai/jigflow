package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jigflow-ai/jigflow/internal/engine"
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
	id := args[0]
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

	by := "a person"
	if p.By != "" {
		by = "agent session " + p.By
	}
	ok, err := e.confirm(fmt.Sprintf("%s from %s: %s\n%sApprove all %s as one unit?", p.ID, by, p.Summary, listItems(p), plural(len(p.Items), "change")))
	if errors.Is(err, errNoTerminal) {
		return fmt.Errorf("approving %s needs confirming in an interactive terminal, but stdin isn't one", p.ID)
	}
	if err != nil {
		return fmt.Errorf("%s: not approved: %v", p.ID, err)
	}
	if !ok {
		return fmt.Errorf("%s: not approved: the approval wasn't confirmed", p.ID)
	}

	// Every Gate of every Transition must pass before anything is saved.
	for _, c := range changes {
		for _, g := range c.Transition.Gates {
			if out, err := e.shell(g.Cmd, c.Artifact.ID, c.From, c.Artifact.Status); err != nil {
				return notApplied(fmt.Errorf("item %d (%s): Gate %q failed (%s: %v)%s", c.Item+1, p.Items[c.Item], g.Name, g.Cmd, err, indent(out)))
			}
		}
	}
	if err := verify(); err != nil {
		return err
	}
	for _, c := range changes {
		if err := st.Save(c.Artifact); err != nil {
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
	id := args[0]
	if e.actor.Agent() {
		return fmt.Errorf("Only a human can reject %s.", id)
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
