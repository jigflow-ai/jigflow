# Wayfinder's Maps are worked through Claims, and the router names the Skills a person starts

The rest of the Pocock Playbook (ADR 0009, 0021) ships every skill in upstream's plugin manifest. wayfinder charts an effort as a map issue with child decision tickets, and parallel sessions keep apart by assigning a ticket before working it. The Playbook makes these the Artifact Types Map and Decision Ticket, kept in GitHub, and makes the assignee a Claim: moving a Decision Ticket from `open` to `in-progress` claims it, which the GitHub Connector records as an assignee, and jfl refuses every other session's move of it, and an agent's start of one still `blocked_by` an open Decision Ticket. Both Statuses are bound to `resolve-decision`, since only a Status with a Binding keeps a Claim, so `jfl next` hands out the frontier too. wayfinder itself stays a Skill only a person starts, as upstream has it: it charts, and, given a Map, chooses the Decision Ticket that `resolve-decision` then works. The map's body can't be written (ADR 0021), so a Map is its description comment and a log of comments, one per decision or scope ruling, which `jfl show` reads in one go. Creating Decision Tickets is a Proposal, since their `open` Status has a Binding; ruling one out of scope or dropping one is a Human Transition. triage's `.out-of-scope/` directory becomes the Out of Scope Type, kept in files, which only a person moves to `reconsidered`.

ask-matt is upstream's router over skills a person has to remember. The router `jfl publish` generates listed only what works on each Status, so it now also names every Skill only a person starts, with its description, which the agent can't otherwise see; that is what replaces ask-matt, for every Playbook.

## Considered Options

- Making wayfinder bound: rejected, since charting starts from a person's idea, and upstream keeps it out of the agent's reach.
- Leaving `open` without a Binding, so agents could create Decision Tickets directly: rejected, since `jfl next` could then never hand out the frontier, and the person should see the chart before it lands in the tracker.
- A Guard that a Map clears only once every Decision Ticket of it is final: deferred, since Guards follow outgoing Links and a Map has none; a person clears it.

## Consequences

- Type names may have spaces (`jfl create "Decision Ticket"`), and a Proposal title with `?`, `:` or `,` must be quoted.
- A Decision Ticket's `resolved` Status is a closed issue with no label, so it comes after `dropped` and `out-of-scope`, which are closed issues with a label, in the Type's Statuses.
- The Playbook's CI refuses a tracker's CLI (issues, pull requests, labels, projects, the API) in a Skill or Guideline, not every `gh`: the wizard template writes CI secrets with it.
