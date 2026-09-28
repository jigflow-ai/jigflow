# playbook-author proposes whole Artifact Types and Skills, checked and simulated before approval

ADR 0004 has the `playbook-author` Skill propose a Playbook the person approves, and ADR 0019 made Proposal items that change the Playbook. Two more kinds of item carry what the Skill proposes: `{type: <name>, text: <YAML>}` declares an Artifact Type, and `{skill: <name>, text: <SKILL.md>}` writes a Skill. Each holds the whole file, and replaces the project's file of that name, or overrides the Base Playbook's by name in a new one named after it. A Type's file already holds its Statuses, Bindings, Transitions, Links and the rest. A Proposal of whole files therefore reads like the Playbook the person will get. Approving writes the files with the Gates and Guidelines, re-checks the Playbook, and puts every file back if it fails its checks or would leave Artifacts in an undeclared Status (ADR 0010).

The proposed Playbook must pass `jfl check`, and the person must see `jfl simulate` of it, before they approve it. So `jfl propose` loads the Playbook as the Proposal's items would make it, in memory over the project's own files, and refuses a Proposal that fails its checks. `jfl check --proposal <id>` and `jfl simulate <Type> --proposal <id>` load a pending Proposal the same way, writing nothing. The Skill runs them and shows their output before asking for approval. `jfl simulate <Type> --source` prints the file declaring the Type, a built-in Base Playbook's too. To change a Type, the Skill copies that file, since an item replaces the whole Type.

`playbook-author` is a Skill only a person starts. It interviews the developer in rounds, as the grilling Skill does, and writes no file under `.jigflow/` itself. The same SKILL.md ships in the Larapilot-style Playbook, built into jfl, and in the Pocock Playbook. `jfl init --playbook own` copies it into a project of its own.

## Considered Options

- Proposal items for single Statuses, Bindings or Transitions: rejected, since each would need its own merge rules and validation, and a person can't read a Type from a list of edits.
- Checking and simulating a candidate Playbook directory the Skill writes: rejected, since the agent would write Playbook files, if not the project's, and the person would approve something other than what was simulated. `--proposal` simulates exactly what is approved.
- Approving, simulating, and reverting on the person's word: rejected, since approval must come after the person sees the simulation.

## Consequences

- A Proposal can't declare a Connector or a Playbook Migration. A tracker-kept Type needs its Connector declared first. A change dropping a Status that Artifacts are in is refused, naming them, until the person writes a Migration or moves them.
- A new Type's file is named after it, so the project's new Types are declared in the order of their names, after the Base Playbook's.
- A Guideline the project already has is still never replaced by a Proposal, while a Type or a Skill is: those are the parts `playbook-author` changes.
