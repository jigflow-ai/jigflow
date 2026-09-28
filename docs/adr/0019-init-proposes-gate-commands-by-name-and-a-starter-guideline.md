# `jfl init` proposes Gate commands by name and a starter Guideline, as changes to the Playbook

A shipped Playbook can't know the language of the project that uses it, and no language packs ship (ADR 0006), so it can't write `go test ./...` on its Transitions. A Gate may therefore be declared by name only (`gates: [{name: tests}]`), and the project's Playbook file gives the Gates of that name their command (`gates: {tests: go test ./...}`), replacing any command the Gate is declared with, the Base Playbook's too. A Gate with no command is not a `check` problem, so the Playbook still loads while the person decides; it refuses the Transition, saying where to give it one, so no work gets past a check nobody configured.

`jfl init` detects the toolchain from the files at the project's root (a Makefile's `test` and `lint` targets first, then Go, Rust, Python, Node.js and PHP tools) and proposes the commands of the Gates `tests` and `lint`, and a starter Guideline, `conventions`, listing the toolchain, those commands, the files that configure how the code is written and the documents to read first. It proposes them as one Proposal from the person, which they approve with `jfl approve` or in the Dashboard, or reject. A shipped Playbook's Skills may name the `conventions` Guideline and ship a general one: the project's overrides it by name.

To carry them, a Proposal item may change the Playbook as well as create or move an Artifact: `{gate: <name>, cmd: <command>}` or `{guideline: <name>, text: <Markdown>}`. Approving writes them into the project's Playbook first, keeping the rest of its Playbook file, re-checks the Playbook and puts every file back if it fails, so a Proposal still applies all of its changes or none. A Guideline the project already has is never replaced. This is the path ADR 0004 means for the whole Playbook: `playbook-author` will propose Artifact Types, Statuses and Bindings as further kinds of item.

The rest of `init` is the person's choice, made directly: it refuses agent sessions. It asks which Playbook to use, with no default (ADR 0009): Larapilot-style, Pocock, or building their own, which starts the `playbook-author` Skill when this build ships it. It asks which Adapter to publish through, and for each Connector keeping an Artifact Type's Artifacts, the settings the Playbook leaves empty and the label of every Status it doesn't map, writing them into the project's Playbook file (ADR 0011); the project's declaration of a Connector replaces the Base Playbook's whole, so `init` first copies the base's. Flags answer every question, for use without a terminal, where a label defaults to the Status's name. Running `init` again keeps the Playbook, republishes through the Adapters that published before, and asks for and proposes only what is missing and not already pending.

## Considered Options

- Proposal items adding a Gate to a named Transition: rejected, since `init` can't tell which Transitions of a Playbook it didn't write should run the tests, and overriding a Base Playbook's Transition means copying its whole Artifact Type into the project.
- `jfl check` refusing a Gate with no command: rejected, since the Playbook couldn't load for the person to approve the Proposal that gives it one.
- `init` writing the Gates and the Guideline directly: rejected, since everything `init` proposes should be something the person can accept or reject, and an agent should be able to propose the same changes later.
- Generating the Guideline's prose from the code: out of reach, since jfl calls no model (ADR 0001); the starter points at what the repository already says, for the person to edit.

## Consequences

- A shipped Playbook should name its Gates `tests` and `lint` where it wants the project's commands.
- Re-encoding the Playbook file to change it keeps its comments and flow style but may re-indent it.
