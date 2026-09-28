# Claude Code's token usage is read from its transcripts, by hooks jfl publishes

The Claude Code Adapter takes token usage from Claude Code's session transcripts, the JSONL files Claude Code writes under its configuration directory (`CLAUDE_CONFIG_DIR`, or `~/.claude`, then `projects/`), with a line per assistant message carrying the `usage` the API reported (`input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`) and the time it was written. `jfl publish claude-code` adds hooks to the committed `.claude/settings.json`, keeping the person's settings and hooks, that run `jfl hook claude-code` at `SessionStart`, `Stop`, `SubagentStop` and `SessionEnd`. At `SessionStart` it writes `export JFL_SESSION=<Claude Code's session id>` to the file `CLAUDE_ENV_FILE` names, so the session's commands, and so its Focus, go by the id its transcript and hooks carry. At the other events it reads the transcript the hook names, and a sub-agent's, and adds to the Ledger, as one entry, each message it doesn't record yet: a message is known by its API id and counted once, since Claude Code writes a line per content block, each with the message's usage.

A message's tokens are charged to the Artifact in its session's Focus when Claude Code says the message was written, or to unattributed. This is the one time in the Ledger that doesn't come from jfl's clock (ADR 0014): a turn often spans several Focus changes (autopilot works through a backlog in one), so the time of the hook would charge the whole turn to wherever it ended. The time comes from Claude Code's records, not from the agent (ADR 0007).

No command or MCP tool takes usage numbers. `jfl hook` reads only files inside Claude Code's records directory, and refuses to run where `JFL_SESSION` is set: Claude Code gives it to the session's shell commands, and (as we understand Claude Code) not to its hooks, so an agent session's `jfl hook` is refused. Like a Human Transition (ADR 0003), this guards against an agent making up usage by accident, not against a determined one that writes a transcript of its own into Claude Code's directory. The Ledger also counts a message once however many entries record it, since a session whose branch changes reads a Ledger that lacks what it recorded on the other branch.

Agents with no usage source, such as those reading AGENTS.md, get no hooks, and the Ledger records their time only; `jfl ledger` shows tokens only once some are recorded.

## Considered Options

- A command or MCP tool through which the agent reports its usage: rejected by ADR 0007.
- OpenTelemetry: Claude Code can export token usage, but only to a collector someone must run and configure on every machine; the transcripts are always there.
- Reading the transcripts when `jfl ledger` runs: rejected because they are on one machine and the Ledger is shared through git (ADR 0014).
- Charging a turn's usage to the Focus at the hook's time: rejected, see above.
- Hooks in `.claude/settings.local.json`: rejected because it isn't committed, so every person would have to publish again.

## Consequences

- An MCP server Claude Code starts gets no `JFL_SESSION`, since `CLAUDE_ENV_FILE` reaches only the session's shell commands; its session's Focus has a fresh id, and the usage of work done through it is unattributed.
- Publishing writes the settings file back with its keys in order.
- The transcript fields above are Claude Code's, not a published contract; the reading skips lines it doesn't understand, so a format change loses usage rather than failing the hook.
