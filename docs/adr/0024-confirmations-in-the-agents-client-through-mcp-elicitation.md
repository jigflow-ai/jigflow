# Human Transitions and approvals can be confirmed in the agent's client through MCP elicitation

A developer who has run `jfl init` should be able to work only in their coding agent, but every Human Transition and every approval sent them to a terminal or the Dashboard: commands the agent runs, and those the person types with Claude Code's `!`, carry `JFL_SESSION` and have no TTY. So a Confirmation, a person's yes to a Human Transition or a Proposal, may now also be given in the agent's client, through MCP elicitation: `jfl mcp` asks the client to show a form, the client shows it to the person and never to the model, and the model sees only the tool result jfl returns. The channel is accepted because the model can't answer it, the bar the TTY's `y` and the Dashboard's click already meet; a "yes" typed in chat, which the model reads and then acts on, is not a Confirmation.

The agent asks for one in two ways: `propose` asks at once whether to approve the Proposal now, and new MCP tools, `approve` and `move` on a Human Transition, ask for pending Proposals and single Human Transitions. jfl writes the form's text itself, from the Proposal or the Transition, never from the agent's summary: the Proposal's summary and one line per item. The form has one required field, `decision: approve | reject` (or `make | refuse` for a Transition), and only an accepted form changes anything; a declined or cancelled one leaves everything pending, for the terminal, the Dashboard or a later ask. No editing happens in the form: to change an item, the person rejects and the agent re-proposes.

`jfl mcp` lists the tools that need a Confirmation only to a client that declared `elicitation` when it connected; to any other, `propose` says what waits for the person and where. It speaks server-sent `elicitation/create` in form mode (MCP 2025-06-18 and 2025-11-25), which Claude Code, Codex, Cursor and VS Code speak today. `jfl publish claude-code` registers `jfl mcp` in the project's `.mcp.json`, and `agents-md` prints the command that registers it for the user's agent. The router Skill, and each Skill that ends in a Proposal or a Human Transition, tell the agent to use those tools where it has them, and the CLI's refusals in an agent session name them.

The Ledger records each Confirmation's channel, `terminal`, `dashboard` or `agent`, on the entry of the Human Transition or approved Proposal it made.

Still outside the agent: `jfl migrate`, rare, all-or-nothing and irreversible, stays with the terminal, and a Transition the Playbook declares `human: dashboard` stays with the Dashboard, since that is its author asking for the out-of-band channel.

## Considered Options

- The person saying yes in chat, or through a question tool such as Claude Code's `AskUserQuestion`: rejected, since the model reads the answer and runs the command, so nothing tells a person's yes from the model's claim of one.
- The coding agent's permission prompt on `jfl approve`: rejected, since bypass-permissions mode skips it, and it lets the agent's own command through rather than the person's decision.
- Declining the form to reject: rejected, since clients decline on their own, Codex with `approval_policy = never` and a Claude Code `ElicitationResult` hook among them, and would reject Proposals no one looked at.
- A form with no fields: rejected, since Codex in Full Access mode accepts those without asking.
- Listing the tools to every client and failing where elicitation is missing: rejected, as offering an agent a tool it can't use.
- Multi round-trip requests (MCP 2026-07-28) now as well: deferred until clients negotiate that version; it changes how the form is asked for, not what it means.

## Consequences

- A Claude Code `Elicitation` hook matching jfl's server answers the form without showing it, and an `ElicitationResult` hook can change the person's answer. `jfl check` and `jfl publish` warn when the project's or the user's settings have such a hook, and the Dashboard shows the warning; the Ledger's `agent` can't tell such an answer from the person's. Human Transitions stay a guardrail, not a security boundary (ADR 0003).
- Agents whose client has no elicitation, Gemini CLI among them, keep the terminal and the Dashboard.
- Claude Code asks the person once to trust the `.mcp.json` server `jfl publish` writes.
