# An agent session may run `jfl init`, with every answer as a flag

Since Confirmations can be given in the agent's client (ADR 0024), everything after `jfl init` can happen in the agent, but `init` itself refused agent sessions (ADR 0019: choosing the project's way of working is the person's, made directly), so a developer who works in their coding agent still had to leave it to set a project up. So an agent session may now run `init`, partly superseding ADR 0019: the person's answers in chat, with the agent they started, are enough for the setup choices, which are the Playbook, the Adapter, each Connector's settings and the label of each Status it keeps. The agent runs `init` as an agent session, with `JFL_SESSION` set, and gives every answer as a flag. There `init` never asks, even at a terminal: an answer missing fails, naming the flag to give (every Connector setting and label still missing at once), and writes nothing, on a first run and when run again in a project already set up, where it asks only for what is still missing and keeps the Playbook chosen.

What `init` proposes, the Gate commands and the starter Guideline (ADR 0019), stays a Proposal, and it is from whoever ran `init`: an agent session's waits for a person's Confirmation at a terminal, in the Dashboard or through the `approve` tool, and the session is refused if it approves it, as for any Proposal of its own. The commands run on every Transition are still the person's to accept. Run by a person at a terminal, `init` works as before.

## Considered Options

- A form through MCP elicitation before `init`, so the setup choices are a Confirmation: rejected, since it needs `jfl mcp` registered with the agent before any project exists, while `init` is what registers it; and the choices are the person's way of working, which they state in chat, not an action by the agent to check.
- Printing the `jfl init` command for the person to run with Claude Code's `!`: rejected, since a command typed there has no TTY, so it must carry every answer as a flag anyway, and the person would be running a long command the agent wrote, which confirms nothing more than their answers in chat did.
- Defaulting what a flag doesn't give, as `init` does without a terminal: rejected, since a label or a setting the agent guessed would be written without the person having seen it; failing names the flag, so the agent asks and runs `init` again.

## Consequences

- Human Transitions and Confirmations stay a guardrail, not a security boundary (ADR 0003): the change is that the setup choices, which ADR 0019 kept to the person at a terminal, may now be made in chat with the agent the person started.
- Before `init` no hook exists to give the session its id, so the agent sets `JFL_SESSION` itself, as AGENTS.md agents do, and keeps it for every `jfl` command until the person restarts the agent.
