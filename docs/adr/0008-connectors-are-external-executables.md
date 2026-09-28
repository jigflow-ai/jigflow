# Each Artifact Type chooses its Store; tracker Connectors are external executables

An Artifact Type's Artifacts live either in repository files or in an outside tracker (e.g. GitHub Issues), so a Playbook can keep work items where the team already works and keep knowledge (PRD, Personas, Guidelines) in git, as Matt Pocock's skills do. The CLI is still the only writer (ADR 0002): it reaches trackers through Connectors, separate programs in any language that exchange JSON over stdin/stdout. Skills never call tracker CLIs like `gh` directly, since that would bypass Guards, Gates, Human Transitions and the Ledger. Connectors map Playbook Statuses to each repo's real labels, and mark text written by an agent as AI-generated.

## Considered Options

- Files as the only truth, with the tracker as a one-way mirror: rejected because it prevents a tracker-first workflow.
- Connectors built into the binary: rejected so vendor API changes stay out of the core release cycle, and so Connectors can be written in any language.
