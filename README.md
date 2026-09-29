# JigFlow

JigFlow (`jigflow`, alias `jfl`) lets you define your own agent workflow as a **Playbook** and run it inside the coding agent you already use: Claude Code, or any agent that reads `AGENTS.md`. It is a layer over the agent, not a new agent.

A Playbook declares the kinds of work in a project (PRDs, Specs, Tickets, Tasks, whatever you call them), the Statuses each one goes through, and the Skill an agent runs in each Status. The `jfl` CLI is the only thing that writes workflow state. An agent asks it for the next piece of work and for every move. `jfl` refuses a move when the tests fail, when the work is blocked, or when the move is one only a person may make.

That last part is why I built it. Prompt-only skill collections ask the agent to wait for a human before merging or approving its own plan, but nothing makes it wait. Opinionated workflow packs do enforce a loop, but it's their loop, tied to their ecosystem. With JigFlow the loop is yours, it works in any language, and the human checkpoints are enforced by the tool.

**Documentation:** [`site/`](site/index.html) — open `site/index.html` in a browser, or serve it with `python3 -m http.server -d site`.

## Install

JigFlow is a single static Go binary with no runtime to install.

```sh
git clone https://github.com/jigflow-ai/jigflow && cd jigflow
make build                      # bin/jigflow, bin/jfl, and the GitHub and Linear Connectors
export PATH="$PWD/bin:$PATH"
```

Or, with Go installed: `go install github.com/jigflow-ai/jigflow/cmd/jigflow@latest`, then `ln -s jigflow "$(go env GOPATH)/bin/jfl"`.

## Try it in five minutes

In an empty repository, with the Larapilot-style Playbook that ships inside `jfl`:

```sh
mkdir todo && cd todo && git init
printf 'module todo\n\ngo 1.22\n' > go.mod
jfl init --playbook larapilot --adapter claude-code
```

```
wrote .jigflow/playbook.yaml, extending the Larapilot-style Playbook
detected Go (go.mod)
proposed P-1: set up the Gates and a starter Guideline for the project's toolchain (3 changes, …)
  1. give Gate "tests" the command go test ./...
  2. give Gate "lint" the command go vet ./...
  3. add Guideline "conventions" (18 lines)
published Playbook "todo" for Claude Code
```

`init` never sets anything up behind your back. What it detects from your toolchain comes as a Proposal, which you approve as a whole or reject:

```sh
jfl approve P-1                 # only a person can approve: it asks you to confirm
jfl create PRD --title "Todo app" --status inception
jfl next
```

```
run /inception on PRD-1 "Todo app"
```

Now open Claude Code in the project and run `/inception`, or ask it to follow the `jigflow` Skill, which is the router `jfl publish` generated. The agent writes the PRD and moves it to `in-review`. Approving a PRD is a Human Transition, yours to make, so the agent asks you: in a form Claude Code shows only to you, through jfl's MCP server, which `jfl init` registered. Put the form off, and it waits for you:

```sh
jfl next                        # nothing for an agent to do — PRD-1: "in-review" has no Binding, so it's human work
jfl move PRD-1 approved         # you confirm it at the terminal, or in the Dashboard
jfl ui                          # the Dashboard, at http://127.0.0.1:7457
```

From there `/spec` turns the PRD into Requirements, `/plan` into Stories and Tasks, and each Task is implemented, reviewed and, when you accept the review, committed on its own.

## Three ways to start

`jfl init` asks which Playbook to use, and there is no default.

- **Larapilot-style** (`--playbook larapilot`), built into `jfl`: PRD → Requirements with MoSCoW priorities → Stories → Tasks, one commit per Task. Artifacts are Markdown files in the repository.
- **Pocock** (`--playbook pocock`): [Matt Pocock's skills](https://github.com/mattpocock/skills) (triage, to-spec, to-tickets, tdd, code-review, wayfinder, …) with their human checkpoints enforced, and Specs, Tickets and Issues kept in GitHub Issues. It lives in its own repository, which isn't published yet: point `init` at a local checkout with `JFL_POCOCK_GIT=/path/to/jigflow-playbook-pocock`.
- **Your own** (`--playbook own`): `init` publishes the `/playbook-author` Skill, which interviews you about how you work and proposes a Playbook. It shows you `jfl check` and `jfl simulate` of the proposed Playbook before you approve it.

## Commands

| | |
|---|---|
| `jfl init` | set the project up: Playbook, Adapter, Connectors, proposed Gates |
| `jfl next [--autopilot]` | which Skill to run on which Artifact |
| `jfl create`, `move`, `comment`, `show`, `query` | work with Artifacts |
| `jfl propose`, `approve`, `reject` | Proposals: changes a person approves as one unit |
| `jfl check`, `simulate`, `migrate` | validate a Playbook (warning about agent hooks that could answer a Confirmation for you), walk an Artifact Type through it, migrate Artifacts after a change |
| `jfl publish <adapter>` | publish the Skills for `claude-code` or `agents-md`, and register `jfl mcp` for the agent: in `.mcp.json` for Claude Code, or by printing the command for an `AGENTS.md` agent |
| `jfl ui`, `ledger` | the local Dashboard; time and tokens per Artifact, and where each Confirmation came from |
| `jfl mcp` | the agent-safe commands as an MCP server, and approving a Proposal, as soon as it is proposed or later, or making a Human Transition, in a form your agent's client shows only to you |

Run `jfl` with no arguments for every flag, or read the [CLI reference](site/cli.html).

## Documentation

- [Getting started](site/getting-started.html) · [Concepts](site/concepts.html) · [Playbook reference](site/playbook.html) · [CLI](site/cli.html)
- [Shipped Playbooks](site/playbooks.html) · [Agents and Adapters](site/agents.html) · [Proposals and the Dashboard](site/proposals.html) · [Connectors](site/connectors.html) · [Ledger](site/ledger.html)
- [CONTEXT.md](CONTEXT.md), the glossary, and the [architecture decision records](docs/adr/) behind each design choice.

## Development

```sh
make test     # go vet ./... and go test ./...
make build
```

- `cmd/jigflow`: the CLI entry point and its end-to-end tests, which run `jfl` as a subprocess
- `cmd/jfl-connector-github`, `cmd/jfl-connector-linear`: the reference Connectors
- `internal/cli`: the commands
- `internal/engine`: the Status machine: Transitions, Readiness, Guards, Claims
- `internal/playbook`: loading and checking Playbooks, and the built-in ones under `builtin/`
- `internal/store`: file and Connector Stores
- `internal/adapter`: the Claude Code and AGENTS.md Adapters
- `site/`: the documentation site, plain HTML with no build step
