package cli

import (
	"fmt"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// autopilot is one step of autopilot for an agent session: it hands out the
// pick of next, like next does, or stops, saying why and what is waiting
// for a person. JigFlow runs no agent loop (ADR 0001): the agent repeats
// the step, running the Skill it names, until it stops.
//
// Autopilot stops when the session's last move was refused, which is how it
// stops at a Human Transition; when next has nothing for an agent; and when
// the Skill it last handed out left its Artifact where it was, which would
// otherwise hand the same work out forever.
func (e *env) autopilot(res engine.NextResult, proposals []engine.Proposal) error {
	sessions := store.NewSessions(e.dir)
	last, err := sessions.Autopilot(e.actor.Session)
	if err != nil {
		return err
	}
	if last != nil && last.Refusal != "" {
		return e.stop(res, proposals, last.Refusal)
	}
	if len(res.Candidates) == 0 {
		return e.stop(res, proposals, "nothing for an agent to do")
	}
	top := res.Candidates[0]
	if last != nil && last.ID == top.Artifact.ID && last.Status == top.Artifact.Status {
		return e.stop(res, proposals, fmt.Sprintf("/%s ran on %s %q, but it is still in %q; nothing moved it on", last.Skill, top.Artifact.ID, top.Artifact.Title, last.Status))
	}
	if err := e.focus(res); err != nil {
		return err
	}
	if err := sessions.SetAutopilot(e.actor.Session, &store.Step{ID: top.Artifact.ID, Status: top.Artifact.Status, Skill: top.Skill}); err != nil {
		return err
	}
	e.pick(top)
	return nil
}

// stop ends autopilot with the reason, and lists what is waiting for a
// person: the Artifacts only they can move on, and the pending Proposals.
func (e *env) stop(res engine.NextResult, proposals []engine.Proposal, reason string) error {
	sessions := store.NewSessions(e.dir)
	if err := sessions.SetAutopilot(e.actor.Session, nil); err != nil {
		return err
	}
	if err := e.setFocus(e.actor.Session, ""); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "autopilot stopped: %s\n", reason)
	var waiting []string
	for _, s := range res.Skipped {
		if s.Person {
			waiting = append(waiting, fmt.Sprintf("%s: %s", s.Artifact.ID, s.Reason))
		}
	}
	for _, p := range proposals {
		if p.Status == engine.Pending {
			waiting = append(waiting, fmt.Sprintf("%s: a pending Proposal to approve or reject: %s", p.ID, p.Summary))
		}
	}
	if len(waiting) == 0 {
		fmt.Fprintln(e.stdout, "Nothing is waiting for a person.")
		return nil
	}
	fmt.Fprintln(e.stdout, "Waiting for a person:")
	for _, w := range waiting {
		fmt.Fprintf(e.stdout, "  %s\n", w)
	}
	return nil
}

// refused records, in the agent session's autopilot run, how its move of id
// to the Status to went: why it was refused, or, when err is nil, that it
// wasn't. Outside a run it records nothing.
func (e *env) refused(id, to string, err error) error {
	sessions := store.NewSessions(e.dir)
	last, rerr := sessions.Autopilot(e.actor.Session)
	if rerr != nil || last == nil {
		return rerr
	}
	last.Refusal = ""
	if err != nil {
		last.Refusal = fmt.Sprintf("jfl move %s %s was refused: %v", id, to, err)
	}
	return sessions.SetAutopilot(e.actor.Session, last)
}
