# The Dashboard changes the Playbook file through Proposals the person confirms as they make them

A person could change the Playbook file (its Gate commands, Mockup folder, Connectors with their settings and mappings, and a git Base Playbook's ref) only by editing YAML by hand, outside the Dashboard, and getting it wrong. The Playbook page was read-only because the Playbook changes through Proposals (ADR 0004). So the Playbook page now edits those values in place, and each edit is a Proposal the person makes and confirms in the same click, under the key `jfl ui` prints (ADR 0017). The edit therefore goes through the one path that already checks a changed Playbook, puts every file back when it fails, and records the Confirmation, with its channel, in the Ledger. Agents may propose the same kinds of item with `jfl propose`, as they already can for a Gate's command (ADR 0019), and a person confirms them as usual. Nothing is committed.

The Dashboard changes values, not structure. Artifact Types, Skills, Guidelines, Personas and switching to another Base Playbook stay with `playbook-author`, `jfl check` and `jfl simulate` (ADR 0023). Setting a project up stays with `jfl init`. The Dashboard offers publishing Skills for another Adapter, and no longer publishing them for one, by running `jfl install` for the person. That is no Proposal, since it changes no workflow state and an agent may already run it.

A value the Base Playbook gives is overridden by name in the project's Playbook file, and an overridden value can go back to the Base's. A Connector is copied whole from the Base before its first edit, since the project's declaration replaces the Base's whole. The form has one field per value, with a toggle for each yes/no value. It shows a Connector's settings as the Connector describes them (ADR 0031).

Some edits reach past the file:
- **Remapping a Status or field value to another label or state** relabels, through the Connector, the tracker's Artifacts that carry the old one, as part of the same Proposal. The Proposal says how many, before the person confirms. When relabelling fails partway, it is reported as a tracker problem and the whole change is undone. This is the first Proposal that reaches a tracker.
- **Re-pointing a Store** (the GitHub Connector's `repo`, Linear's `team`) is allowed. The Proposal says how many Artifacts will no longer be seen, since nothing jfl can do keeps them.
- **Moving the Mockup folder** is refused while the old folder holds Mockups, since bodies reference them by path and the Dashboard never edits a body.
- **An edit to a value that a pending Proposal also changes** goes through, and the form says so with a link to that Proposal. Approving that Proposal later shows the value it replaces.

## Considered Options

- A `jfl` command that writes a value directly, which the Dashboard runs for the person: rejected. It needs its own Confirmation, or an agent could run it to set a Gate's command to `true`, and it would duplicate the checks, rollback and Ledger entry a Proposal already has.
- The Dashboard writing the YAML itself: rejected by ADR 0002 and ADR 0017.
- The Playbook file's YAML in a text box, checked on submit: rejected, since getting the YAML right by hand is the problem this solves.
- Refusing a remapping while the tracker holds Artifacts with the old label: rejected as blocking the everyday case. It remains the fallback, should a Connector be unable to relabel.
- Editing Types and Skills in the Dashboard too: rejected, since they would skip the simulation a person sees before approving them.

## Consequences

- Proposal items gain kinds for the Mockup folder, a Connector's command, args, marker, settings and mappings, and a git Base's ref, next to a Gate's command.
- A Proposal can now fail as a tracker problem, not only as a workflow refusal.
