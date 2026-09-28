# JigFlow

A language-agnostic tool for defining your own spec-driven, human-in-the-loop agent workflow and running it inside existing coding agents. Inspired by Larapilot, but not tied to Laravel or PHP.

## Language

### Playbook

**Playbook**:
The complete user-defined way of working for a project: its Artifact Types, Statuses, Transitions, Gates, Skills and Personas. The shipped default Playbook is written in the same format and has no special privileges.
_Avoid_: Framework, workflow, pack, methodology

**Base Playbook**:
The single Playbook another Playbook extends; the extending Playbook overrides its parts by name.
_Avoid_: Parent, template, preset

**Playbook Migration**:
A declared mapping from old Statuses to new ones, applied to existing Artifacts when a Playbook or its Base Playbook changes.
_Avoid_: Upgrade, schema change

**Adapter**:
The part that publishes a Playbook's Skills in the format a particular coding agent expects.
_Avoid_: Integration, plugin, connector

### Workflow state

**Artifact**:
A file in the repository that holds one unit of workflow state (e.g. a requirement, a story, a task) and is the source of truth for it.
_Avoid_: Document, record, ticket

**Artifact Type**:
A user-declared kind of Artifact, with its own fields, its own set of Statuses, and its own Store.
_Avoid_: Schema, template, entity

**Store**:
Where the Artifacts of one Artifact Type live: files in the repository (the default) or an outside tracker reached through a Connector.
_Avoid_: Backend, storage, repository

**Status**:
The current position of an Artifact in the lifecycle declared by its Artifact Type.
_Avoid_: State, stage, phase

**Readiness**:
A declared condition on a Status that must hold before agent work on an Artifact in that Status can start (e.g. every *blocked_by* Link is done).
_Avoid_: Precondition, blocked flag

**Inbox**:
A Status where agents may create Artifacts directly without a Human Transition. It is valid only if no Skill that changes code or Artifacts can be reached from it without passing a Human Transition.
_Avoid_: Queue, backlog, intake

**Transition**:
A declared move of an Artifact from one Status to another.
_Avoid_: Step, promotion

**Gate**:
A user-declared command that must succeed for a Transition to happen.
_Avoid_: Check, hook, quality gate

**Guard**:
A declared condition on the Statuses of Linked Artifacts that must hold for a Transition to happen. Unlike Readiness, it is checked only at the moment of the move.
_Avoid_: Rule, precondition, constraint

**Link**:
A typed, declared relation from one Artifact to another (e.g. a Story *implements* a Requirement).
_Avoid_: Reference, relation, parent

**Action**:
A user-declared command run after a Transition succeeds (e.g. commit, open a branch, notify).
_Avoid_: Hook, effect, callback

**Human Transition**:
A Transition that only a human may perform; the agent can propose it but never make it. Creating an Artifact directly into a Status that has a Binding also counts as a Human Transition, unless that Status is an Inbox. It guards against accidents, not against a determined agent.
_Avoid_: Approval step, manual step

**Focus**:
The Artifact a session is currently working on; set by `next` and moved by Transitions. It lives only in the session, never in git.
_Avoid_: Claim, current task, active item

**Proposal**:
A set of one or more Artifacts or Transitions an agent puts forward together, approved or rejected by a human as one unit.
_Avoid_: Batch, changeset, request

**Claim**:
A recorded owner on an Artifact, stored in the tracker's assignee or in the file, so parallel sessions don't pick the same work. Unlike a Focus, it is shared. It is released automatically when the Artifact enters a Status with no Binding or a final Status.
_Avoid_: Lock, assignment, reservation

### Visibility

**Ledger**:
The committed record of the time each Artifact spends in each Status, and of agent session time, and tokens where the agent's own records expose them (never numbers the agent reports), charged to the Artifact in Focus or to unattributed.
_Avoid_: Usage log, metrics, telemetry

**Connector**:
An external executable, written in any language, through which the CLI reads and writes Artifacts kept in an outside tracker, mapping Statuses to that tracker's labels or states.
_Avoid_: Integration, plugin, sync

**Dashboard**:
The local web view of a project's Artifacts, human queue, pending Proposals, Ledger and Status machines, served only on the developer's machine, and the preferred place to perform Human Transitions.
_Avoid_: UI, console, admin panel

### Agent guidance

**Skill**:
A SKILL.md prompt file that tells an agent how to do one piece of work in the workflow; it may request Transitions but never write state directly.
_Avoid_: Command, prompt, recipe

**Invocation Mode**:
How a Skill may be started: by a person only (user), by the agent whenever relevant (agent), or through a Binding (bound).
_Avoid_: Trigger, activation

**Binding**:
The declared association between a Status of an Artifact Type and the Skill that works on Artifacts in that Status. The set of Bindings is the Playbook's topology; a Status with no Binding is human work and is never handed to an agent. A Binding may ask for the Skill to run in a fresh session.
_Avoid_: Route, handler, pipeline, orchestration

**Guideline**:
A rule file in the Playbook that Skills reference and load only when needed; the home of language and codebase conventions.
_Avoid_: Runtime pack, rules, convention file

**Persona**:
A named point of view a Skill asks the agent to adopt; it is not a separate agent and has no memory of its own. A Persona is itself an Artifact: the agent may propose one on demand, but it becomes usable only through a Human Transition.
_Avoid_: Role, agent, character

**Persona Library**:
A user-level collection of Personas reused across projects; a project Persona with the same name overrides a library one.
_Avoid_: Persona registry, persona pack
