# Connector protocol

An Artifact Type can keep its Artifacts in an outside tracker (GitHub Issues, Linear, …) instead of files in the repository. `jfl` stays the only writer (ADR 0002) and reaches the tracker through a **Connector**: an external executable, in any language, that speaks the JSON protocol below over stdin and stdout (ADR 0008).

This document is for people writing a Connector, and for people setting one up in a project. Two reference Connectors ship alongside jfl: `jfl-connector-github` for GitHub Issues, described in [github-connector.md](github-connector.md), and `jfl-connector-linear` for Linear, described in [linear-connector.md](linear-connector.md), each with its settings and how it maps Artifacts to issues.

## Setting up a Connector

An Artifact Type names its Store. `files` is the default; any other name is a Connector declared in the project's Playbook file.

```yaml
# .jigflow/types/ticket.yaml
name: Ticket
prefix: T
store: github
statuses: [needs-triage, ready-for-agent, in-progress, in-review, done]
fields:
  category: [bug, enhancement]
# …
```

The Connector and the project's settings for it live in `.jigflow/playbook.yaml`, next to `extends`. A Base Playbook may declare Connectors too; the project's own Playbook file overrides one by name, like every other part.

```yaml
# .jigflow/playbook.yaml
name: shop
extends: {builtin: pocock}
connectors:
  github:
    command: jfl-connector-github  # on PATH, or a path relative to the project root
    args: []
    settings: {repo: acme/shop}    # sent to the Connector as they are
    marker: "_Drafted by an AI agent._"
    types:
      Ticket:
        settings: {label: ticket}  # merged over the Connector's settings for Tickets
        statuses:
          in-progress: "status: doing"  # a label
          done: {state: closed}         # a state; {label: …, state: …} for both
        fields:
          category:
            enhancement: "kind: feature"
```

- **`statuses`** maps a Playbook Status to the tracker's label or state. A Status it doesn't map is a label with the Status's name, so a tracker whose labels already match the Playbook needs no mapping.
- **`fields`** maps each value of a field, like `category`, the same way. A value it doesn't map is a label with the value's name.
- **`marker`** is the AI-generated marker; without it, jfl uses `_Written by an AI agent through JigFlow._`.
- **`settings`** are the Connector's own (a repository, a team, a label that tells Tickets from Specs), sent in every request. Credentials don't belong here, since the Playbook file is committed: a Connector reads them from its environment, e.g. `GH_TOKEN`, and jfl runs it with its own environment.

`jfl check` reports a Store that names no Connector, a Connector with no command, and settings that map an Artifact Type the Connector doesn't keep, a Status or field value that Type doesn't declare, or a Status or value mapped to no label or state.

## Who does what

jfl does the Playbook's work, so every Connector behaves the same and a Connector knows nothing of Statuses, Transitions or sessions:

- **jfl maps** Statuses and field values to labels and states, and back, using the project settings. The Connector only sees labels and states.
- **jfl adds the AI-generated marker** to text an agent writes into the tracker, before sending it. The Connector writes text as it gets it. This way no Connector can forget the marker.
- **jfl names Artifacts** by their Artifact Type's prefix and the tracker's own id: the tracker's item `41` is the Ticket `T-41`. A tracker gives an Artifact its id when it creates it.
- **The Connector** talks to the tracker: it finds the items of an Artifact Type, reads and writes their labels, state and assignee, and reports failures.

## Messages

jfl runs the Connector once per request, in the project root, writes one JSON request to its stdin and closes it, then reads one JSON response from its stdout. The Connector exits 0 once it has written a response, including an error response. stderr is free for the Connector's own diagnostics: jfl shows it only when the Connector breaks the protocol.

### Requests

Every request carries:

| field | |
|---|---|
| `protocol` | `1`, the protocol version |
| `op` | the operation: `list`, `get`, `create`, `status`, `claim` or `comment` |
| `type` | the Artifact Type the request is about, e.g. `"Ticket"` |
| `settings` | the Connector's settings, with the Type's merged over them |

and, per operation:

| `op` | fields | response |
|---|---|---|
| `list` | | `{"items": [item, …]}`: every item of the Type |
| `get` | `id` | `{"item": item}` |
| `create` | `item`, without `id` | `{"item": item}`: the item as created, with the tracker's `id` |
| `status` | `id`, `from`, `to` | `{}` |
| `claim` | `id`, `claim` | `{}` |
| `comment` | `id`, `body` | `{}` |

- **`status`** changes an item's Status. `from` and `to` are each `{"label": …, "state": …}`, either field optional: remove the `from` label, add the `to` label, creating it if the tracker needs that, and set the `to` state when there is one.
- **`claim`** records the agent session that claims the item, e.g. in its assignee; `""` clears the Claim. `get` and `list` must report the same session back as the item's `claim`, since jfl compares it with other sessions.
- **`comment`** adds a comment whose text is `body`, Markdown.

An **item** is:

```json
{
  "id": "41",
  "title": "Reset-token table",
  "body": "…",
  "labels": ["ready-for-agent", "bug"],
  "state": "open",
  "claim": "agent-1727000000",
  "links": {
    "blocked_by": [{"id": "40"}],
    "part_of": [{"artifact": "S-3"}]
  }
}
```

Only `id` and `title` are required in a response. A Link's target is `{"id": …}`, the tracker's id, when the Link's Artifact Type is kept by the same Connector (a Connector may store those as the tracker's native relations, like GitHub's issue dependencies), and `{"artifact": …}`, the jfl Artifact id, when it lives elsewhere (e.g. a Spec kept in files). jfl sends Links only on `create`.

Examples:

```json
{"protocol": 1, "op": "status", "type": "Ticket", "settings": {"repo": "acme/shop"},
 "id": "41", "from": {"label": "ready-for-agent"}, "to": {"label": "status: doing"}}
```

```json
{"protocol": 1, "op": "create", "type": "Ticket", "settings": {"repo": "acme/shop"},
 "item": {"title": "Reset endpoint", "labels": ["ready-for-agent"], "body": "_Written by an AI agent through JigFlow._",
          "links": {"blocked_by": [{"id": "41"}]}}}
```

### Reading an item back

jfl reads an item's Status as the first of its Artifact Type's Statuses, in declaration order, whose label the item has and whose state it is in. An item that matches none, like one a teammate filed in the tracker without labels, is in the Type's first initial Status. A field is the first of its values whose label or state the item has, and has no value when none matches.

### Errors

A Connector that can't do what was asked answers with an error instead:

```json
{"error": {"kind": "rate_limit", "message": "API rate limit exceeded", "retry_after": 60}}
```

| `kind` | meaning |
|---|---|
| `auth` | the tracker didn't accept the Connector's credentials |
| `rate_limit` | the tracker is limiting requests; `retry_after`, in seconds, is optional |
| `network` | the tracker couldn't be reached |
| `not_found` | the tracker has no item with that `id` |
| `invalid` | the tracker, or the Connector, refused the request as it was made |
| `internal` | anything else |

jfl reports `not_found` as it reports any unknown Artifact. It reports every other kind, and a Connector that can't be run, exits non-zero or doesn't answer with JSON, as a **tracker problem, not a workflow refusal**, with exit status 3:

```
jfl move: tracker problem, not a workflow refusal: Connector "github" was rate-limited by the tracker: API rate limit exceeded. Retry in 60s.
```

A workflow refusal (an undeclared Transition, a failing Gate or Guard, a Human Transition asked by an agent, another session's Claim) exits 1, as for Artifacts in files.

## Edits made in the tracker

Artifacts in files carry a content hash, so jfl can re-validate edits made outside it: a body edit is allowed, and a frontmatter edit refuses the next Transition. That doesn't apply to a tracker. Teammates work in the tracker too, so it is the source of truth for its Artifacts: its bodies and comments are free to edit, and a Status or Claim changed there is where jfl's next move starts from.

What jfl does check is that the tracker didn't change under it while a command was running: before writing, a move reads the item again after its Gates, and refuses the move, writing nothing, if its Status or Claim changed since jfl read it.

## Operations and atomicity

A move is two requests at most: `status`, then `claim` when the Claim changes. Approving a Proposal creates and moves its items one request at a time, after every Gate has passed; a tracker has no transaction, so a Connector failing part way leaves the items before it applied, and jfl reports the tracker problem.
