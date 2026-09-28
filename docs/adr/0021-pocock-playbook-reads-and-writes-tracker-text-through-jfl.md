# The Pocock Playbook reads and writes tracker text through jfl, and leaves triage to the maintainer

The Pocock Playbook (ADR 0009) lives in its own repository, `jigflow-playbook-pocock`, and `jfl init --playbook pocock` extends it by git URL, pinned to a release tag; `JFL_POCOCK_GIT` and `JFL_POCOCK_REF` name a fork or a mirror instead, which is also how the tests point it at a repository of their own. Its Artifact Types are Spec, Ticket and Issue, kept as issues of one GitHub repository and told apart by the labels `spec`, `ticket` and `issue`, and ADR, kept in files; CONTEXT.md stays a plain file. Its UPSTREAM file records the upstream commit and which upstream file each adapted Skill or Guideline comes from, one space-separated record per line, for the sync script. The upstream Skills' reference files become Guidelines, since a Skill is published as its SKILL.md alone.

Upstream reads issues and writes their bodies with `gh`, which a Skill must never call (ADR 0008). So `jfl show <id>` prints an Artifact with its body and, for one kept in a tracker, its comments, which a Connector's `get` may now report; the MCP server offers it too. jfl still writes no tracker bodies (ADR 0020): a Spec's and a Ticket's description, and an Issue's agent brief, are comments the Skill posts with `jfl comment` once the person approves the Proposal that creates the Artifact, and `show` is how the next Skill reads them.

Upstream's `triage` is invoked by the maintainer, and every outcome but asking the reporter for more is theirs to decide. `needs-triage`, the Issue's Inbox, therefore has no Binding: `triage` is a user-invoked Skill that recommends, posts notes and briefs and moves an Issue to and from `needs-info`, while the moves to `ready-for-agent`, `ready-for-human` and `wontfix` are Human Transitions. Tickets and Issues an agent works share one loop, as the Larapilot-style Task's: `implement`, the Gates `tests` and `lint`, `code-review` in a fresh session, and a Human Transition from `ready-to-merge` to `done` that commits the work.

## Considered Options

- Binding `triage` to `needs-triage`: rejected, since `triage` writes `.out-of-scope/` and CONTEXT.md, so it changes files, and an Inbox can't reach such a Skill without a Human Transition.
- A Connector operation to write an issue's body: deferred, since every Connector, the fake ones and the Dashboard would need it, and comments already carry the text where the tracker shows it.
- Leaving the Issue Type unlabelled, so issues filed with no label are Issues: rejected, since every Spec and Ticket issue would then be an Issue too.
- Keeping a copy of the Playbook in this repository for its tests: rejected, since it would drift from the repository it copies. Tests that need the Playbook itself use a checkout next to this repository, or `JFL_POCOCK_PLAYBOOK`, and skip without one; the Playbook's own CI runs `jfl check` and `jfl simulate` for every Type.

## Consequences

- A teammate's issue shows up for triage only once it has the `issue` label.
- jfl has no command to change a field, so an Issue's category is given when it is filed, or by the maintainer in the tracker.
- `jfl init --playbook pocock` fetches the Playbook's repository, and fails, leaving the project as it was, where it can't.
