# Personas the Playbook or the Library ships are usable; those an agent proposes need activating

ADR 0004 makes a Persona an Artifact of a built-in Type, which an agent proposes and a Human Transition activates. Every Playbook therefore has the Artifact Type `Persona` (ids `PERSONA-<n>`): its title is the Persona's name, the name Skills refer to, and its body the Markdown describing it. Its Statuses are `proposed`, an Inbox where anyone may file one, since nothing there is agent work, and `active` and `retired`, both final, since no work waits on a Persona there. Every Transition between them is a Human Transition, so only a person activates, retires or brings back a Persona, in a terminal or in the Dashboard. A Playbook can't declare a Type of that name or prefix, and a name is one Persona: a second Artifact of the same name is refused, and a retired one is brought back instead.

The Personas a Playbook ships (`.jigflow/personas/<name>.md`) and those of the user's Persona Library (`$XDG_CONFIG_HOME/jigflow/personas/<name>.md`, or `~/.config/…`) are not Artifacts and need no activating: a person wrote them, and putting them there is the decision a Human Transition would record. A Persona of the project overrides the Library's of the same name: one the Playbook ships, or a Persona Artifact a person has decided on, active or retired, which also overrides the Playbook's. A retired one retires the name in this project rather than bringing the Library's back; a proposed one overrides nothing until it is activated.

`jfl publish` resolves the Personas usable on this machine and publishes only those: next to each Skill that names one, as Guidelines are, and all of them for the agent to adopt when a person or the conversation asks, in the router Skill for Claude Code and in `.agents/personas` for the AGENTS.md fallback. For a Persona a Skill names that isn't usable, the published Skill tells the agent to work without it and how to propose it from the Skill's fallback description: `jfl create Persona --title <name>`, then the description in the body of the file it creates.

## Considered Options

- Shipped Personas as Artifacts too, each to activate: rejected as ceremony for a Playbook that ships five or six, whose author already chose them; a person who doesn't want one retires it.
- A proposed Persona overriding the Library's at once: rejected, since an agent's proposal would take away a Persona the person relies on before anyone decided on it.
- `jfl check` looking up the Library: rejected, since the Library is on one machine and `check` runs in CI too; a Skill naming a Persona the Playbook doesn't ship still needs a fallback description.
- A `--body` for `jfl create`: not needed, since people and agents may edit any Artifact's body (ADR 0002).

## Consequences

- What `jfl publish` writes depends on the Library of the machine it runs on; publishing on another machine may add or drop the Library's Personas.
- A Persona Artifact is a file whatever Store the Playbook's other Types use, so a tracker never holds Personas.
