---
description: Build this project's Playbook, or change the one it extends, by describing how you work. An interview that proposes Artifact Types, Statuses and Bindings for you to approve.
changes: true
invocation: user
---
Build the project's Playbook from how the person works, or change the one it has. They describe their way of working; you turn it into Artifact Types, Statuses, Bindings and the Skills those Bindings name. You propose the result as one Proposal. You never write a file under `.jigflow/` yourself: the Playbook changes only when the person approves the Proposal.

## 1. Find out what exists

Finding facts is your job, never the person's. Before asking anything:

- Read `.jigflow/playbook.yaml`. With no `extends:` and no Types, you are building a Playbook from scratch. With `extends:`, the person keeps that Base Playbook and you propose changes to it. A Type or Skill the project declares replaces the Base Playbook's one of the same name.
- Run `jfl check`. Then run `jfl simulate '?'`, which fails but lists the declared Types, and `jfl simulate <Type> --source` for each one. That prints the file declaring the Type, the Base Playbook's too, and every path from an initial Status to a final one.
- Read the Skills the Playbook ships. The project's own are in `.jigflow/skills/`. Published copies are wherever the coding agent keeps its Skills, such as `.claude/skills/` or `.agents/skills/`. A git Base Playbook is also checked out under `.jigflow/cache/git/<commit>/`, at the commit `.jigflow/playbook.lock` pins.
- Run `jfl query` to see which Statuses Artifacts are in. Changing a Type must not drop a Status an Artifact is in (see Rules).
- Look at the repository: its languages, its test and lint commands, its CI, and whether it uses an issue tracker.

## 2. Interview the person

Interview the person until you both understand how they work. Map it as a design tree, where every decision branches into the decisions that hang off it. Work in rounds. In each round, ask every question whose prerequisites are already settled. Number each one and give your recommended answer:

```
❓ **Q1** - **<question title>**: <the question, with the choices you see>

➡️ <your recommended answer, and why>
```

Then wait for their answers before the next round. A question that depends on another question still open belongs to a later round. Ask in the person's words, not JigFlow's: "what happens to a bug report after someone files it?", not "what are the Statuses of Issue?". When an answer settles a JigFlow concept, name it once, such as "so a *Human Transition* from review to done".

The tree, roughly in this order:

- **Units of work** → Artifact Types. What do they track: ideas, specs, stories, tasks, bugs, decisions? For each unit, ask whether it is kept in files in the repository or in a tracker the Playbook file already declares a Connector for. Ask whether it has a field with a fixed set of values, such as a priority or a category, and how it relates to other units: part of one, blocked by another (Links).
- **Lifecycle** → Statuses. For each unit, ask where it starts (initial), the steps it goes through, and where it ends (final): done, dropped, rejected.
- **Who works each step** → Bindings. In each Status, ask whether an agent does the work, with which Skill, or a person, with no Binding. Ask whether that Skill should start in a fresh session, as a review does.
- **Checkpoints** → Human Transitions, Gates and Actions. Ask which moves only a person may make, such as approving a spec, accepting a review or dropping work. Ask which commands must pass before a move: Gates, named `tests` and `lint` where they are the project's test and lint commands. Ask what runs after a move, such as a commit: Actions.
- **Waiting and conditions** → Readiness and Guards. Ask whether a unit waits for others to be done before an agent starts it, and whether a move needs its linked units in some Status.
- **Intake** → Inbox. Ask where anyone, agents included, may file work without a person approving it first.
- **Parallel sessions** → Claims. Ask whether several agent sessions work at once. Then the Status they work in needs a Binding, since only a Status with a Binding keeps a Claim.
- **The Skills** each Binding names: what each one does, step by step, which `jfl` commands it runs, and where it stops for the person. Also ask for Skills only a person starts (`invocation: user`) and reference Skills the agent reaches on its own (`invocation: agent`).

The interview is done when every branch is settled and nothing is silently assumed. Summarise the way of working back in their words, and wait for them to confirm it before you draft.

## 3. Draft the Playbook

Write each Artifact Type as the YAML of its file:

```yaml
# A Task: one change to the code, small enough to review on its own.
name: Task
prefix: T                        # its Artifacts' ids: T-1, T-2, ...
store: files                     # or the name of a Connector the Playbook file declares
fields:
  priority: [must, should, could]
links:
  part_of: Story                 # Link name: the Artifact Type it points to
  blocked_by: Task
statuses: [ready, in-progress, in-review, reviewed, done]
initial: [ready]
final: [done]
inbox: []                        # Statuses agents may create into directly
bindings:
  ready: {skill: implement, fresh: true}
  in-progress: implement
  in-review: {skill: review, fresh: true}
readiness:
  ready:
    - {kind: linked-all-in, link: blocked_by, statuses: [done]}
transitions:
  - {from: ready, to: in-progress}
  - from: in-progress
    to: in-review
    gates:
      - {name: tests}            # by name only: the project gives its command
      - {name: lint}
  - {from: in-review, to: in-progress}
  - {from: in-review, to: reviewed}
  - {from: reviewed, to: done, human: true}   # human: dashboard to require the Dashboard
```

Guards sit on a Transition like Gates do: `guards: [{kind: linked-all-in, link: part_of, statuses: [done]}]`, or `{kind: has-incoming, link: implements, min: 1}` for Artifacts that Link to this one. An Action is `actions: [{name: commit, cmd: <shell command>}]`, and it sees the Artifact as `$JFL_ARTIFACT` and `$JFL_TITLE`.

Write each Skill as its SKILL.md, frontmatter first:

```markdown
---
description: Implement the Task in Focus test-first, then move it to in-review.
changes: true
invocation: bound
guidelines: [conventions]
---
Implement the Task in Focus. ...
```

- `invocation`: `bound` for a Skill a Binding names, which `jfl next` hands out on the Artifact in Focus. `user` for one only a person starts, with a one-line description for the person. `agent` for one the agent reaches whenever relevant, with a description that says when.
- `changes`: whether it changes code or Artifacts.
- `guidelines`: the Guidelines it may load, which it names in its body as "the <name> Guideline". A Skill is published as its SKILL.md alone, so its reference material goes in a Guideline.
- `personas`: the points of view the agent takes while running it, each a `name` and a one-line `fallback` description, used to propose the Persona in a project that doesn't have it.

A bound Skill reads its Artifact with `jfl show <id>` and moves it on with `jfl move <id> <status>`. It writes in a tracker only through jfl (`jfl comment`, `jfl create`, `jfl propose`), never with the tracker's own CLI. When it creates Artifacts into a Status that has a Binding, it puts them in a Proposal for the person to approve. Don't write a router Skill: `jfl publish` generates one from the Playbook.

A Skill that names several Personas can have the agent attribute its comments: one comment per Persona it applies, on the Artifact in Focus, with `jfl comment <id> --persona <name> <text>`, so the person sees that each point of view was taken and what it said. Write that into the step where the Skill comments. A Persona that found nothing says what it checked. A Persona the Skill names that isn't usable in the project, which jfl refuses, gets a plain `jfl comment <id> <text>`, without `--persona`, saying it couldn't be applied. A Skill with one Persona leaves its comments plain.

A Skill that ends in a Proposal or a Human Transition ends by asking the person for their Confirmation. If jfl's MCP tools include `approve`, the agent asks them in a form only they see: with the `propose` tool, which asks at once, for a Proposal, or the `approve` tool for one already pending, as section 5 does; with the `move` tool for a Human Transition. Otherwise it tells them what waits for them and where: `jfl approve <id>` or `jfl reject <id>` for a Proposal, `jfl move <id> <status>` for a Human Transition, in their own terminal, or the Dashboard (`jfl ui`). Write that into the step where the Skill's work ends, naming the Status or the Proposal.

## 4. Propose it

Put the whole Playbook, or the whole change, in one Proposal file. Use one item per Type and per Skill, and add Guidelines and Gate commands if they are needed:

```yaml
summary: a Playbook for specs broken into tasks
items:
  - type: Task
    text: |
      name: Task
      prefix: T
      ...
  - skill: implement
    text: |
      ---
      description: ...
      ---
      ...
  - {guideline: review-checklist, text: "# Review checklist\n..."}
  - {gate: tests, cmd: go test ./...}
```

A `type` item replaces the whole Type of that name, so to change a Type the Base Playbook declares, copy its file from `jfl simulate <Type> --source` and change that copy. A `skill` item replaces the whole Skill. A Guideline the project already has can't be replaced by a Proposal: the person edits that file. Propose only what changes.

Run `jfl propose <file>`, the command rather than jfl's `propose` tool, which would ask the person before they have seen section 5. jfl refuses a Proposal whose Playbook would fail `jfl check`, listing every problem. Fix each problem and propose again.

## 5. Show it before asking for approval

With the Proposal pending as `P-<n>`:

1. Run `jfl check --proposal P-<n>`.
2. Run `jfl simulate <Type> --proposal P-<n>` for every Type the Proposal declares or changes.
3. Show the person that output as it is. Then walk them through each path in their own words: who works each Status, where they decide, what must pass. Ask whether it matches how they work.
4. Only then ask them for their Confirmation. Tell them they can read every item in `.jigflow/proposals/P-<n>.yaml`. If jfl's MCP tools include `approve`, use the `approve` tool on P-<n>: it asks them to approve or reject it in a form only they see, and its result says what they decided. Otherwise tell them it waits for them: `jfl approve P-<n>` or `jfl reject P-<n>` in their own terminal, or the Dashboard (`jfl ui`). You can't approve it, and must not try.

If they want changes, revise the draft, propose again, check and simulate the new Proposal, and ask them to reject the old one, in the same way. After approval, tell them to run `jfl init` again. It sets up the Connectors of tracker-kept Types, proposes the Gates' commands, and republishes the Skills. If they reject the Proposal, ask why and go back to the interview.

## Rules the Playbook must respect

`jfl check` enforces most of these:

- Every Status is reachable from an initial one, and every Status that isn't final has a Transition out.
- Every Binding names a Skill the Playbook has, so propose the Skills with the Types that bind them.
- An Inbox must not reach a Skill with `changes: true` without passing a Human Transition. Creating into a Status that has a Binding, and isn't an Inbox, is itself a Human Transition, so agents propose such creations.
- A Status with no Binding is a person's queue, and `jfl next` never hands it to an agent. Leave a passing review in an unbound Status (such as `reviewed`), so a person accepts it, rather than in one bound to the reviewing Skill.
- Only a Status with a Binding keeps a Claim. A Claim is released when the Artifact enters a Status with no Binding or a final one.
- A Type kept in a tracker names, as its `store`, a Connector the Playbook file declares. The Proposal can't declare one, so ask the person to add the Connector first, or keep the Type in files for now. When several of its Statuses map to closed issues in GitHub, the one with no label must come last in the Type's `statuses`.
- A Type's `name` and `prefix` are unique, and `Persona` and its prefix are built in. Links, Readiness and Guards name only Links the Playbook declares.
- A change that drops or renames a Status an Artifact is in doesn't load until a Playbook Migration maps the old Status to a new one. jfl refuses to propose it, so tell the person which Artifacts are in the way. They write the migration in `.jigflow/migrations/` or move those Artifacts first.
- A Playbook adapted from an outside source, as the Pocock Playbook adapts Matt Pocock's skills, is best kept in its own repository. Pin it to the upstream commit it adapts, list which upstream file each Skill and Guideline comes from, and use a script that shows upstream's changes since then. The Pocock Playbook's UPSTREAM file and `scripts/sync-upstream.sh` show the pattern.
