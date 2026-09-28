# Linear Connector

`jfl-connector-linear` keeps the Artifacts of an Artifact Type as issues of a Linear team. It is a reference Connector: it ships alongside `jfl` (`make build` puts it in `bin/`), and it speaks the [Connector protocol](connector-protocol.md) like any other, against Linear's GraphQL API.

## Setting it up

Put `jfl-connector-linear` on `PATH`, or give its path relative to the project root, and declare it in `.jigflow/playbook.yaml`. A Linear team usually keeps its workflow in workflow states, so map each Status to one:

```yaml
connectors:
  linear:
    command: jfl-connector-linear
    settings:
      team: ENG              # the team's key, required
      create_labels: false   # true to create labels the team lacks
    types:
      Ticket:
        settings: {label: ticket}
        statuses:
          needs-triage: {state: Backlog}
          ready-for-agent: {state: Todo}
          in-progress: {state: In Progress}
          in-review: {state: In Review}
          done: {state: Done}
        fields:
          category:
            enhancement: "kind: feature"   # a label
```

A Status can be a label instead, or both (`{label: …, state: …}`), as for any Connector. A state is a workflow state's name, exactly as the team's settings in Linear show it.

| setting | |
|---|---|
| `team` | the team's key, the prefix of its issue identifiers (`ENG` in `ENG-41`). Required. |
| `label` | the label that tells this Artifact Type's issues apart, set per Type. The Connector lists and gets only the team's issues with it, and adds it to the issues it creates. Without it, every issue of the team is one of the Type's, so at most one Type can go without it. |
| `create_labels` | `true` creates a label the team doesn't have when an issue needs it, as a team label. By default the Connector refuses the request instead (an `invalid` error naming the label), so a mistyped mapping doesn't fill the team with labels. A label can be the team's or the workspace's. |
| `assignee` | the display name of the Linear user a Claim assigns. By default, the user the API key belongs to. |

The API key comes from the environment jfl runs in, `LINEAR_API_KEY`, never from the Playbook file. Create a personal API key in Linear under *Settings → Security & access*; it needs to read and write the team's issues. The key is sent as it is in the `Authorization` header, so an OAuth access token works too, as `LINEAR_API_KEY="Bearer <token>"`. `LINEAR_API_URL` overrides the GraphQL endpoint, `https://api.linear.app/graphql`.

## How Artifacts map to issues

- **Ids** are the issues' numbers in the team: `ENG-41` is the Ticket `T-41`.
- **Status and fields** are labels, or workflow states, as the project settings map them (jfl does the mapping, ADR 0011). One move is one update of the issue, its labels and state together. An issue created in no state is in the team's default state for new issues; so is an issue moved out of a Status with a state into one without, as a GitHub issue is reopened.
- **Title and body** are the issue's title and description, free to edit in Linear.
- **Claims** are assignees. A Claim assigns the issue to the `assignee` setting, or the API key's user, and records the agent session in a *JigFlow* attachment of the issue (ADR 0013), which Linear shows as "Claimed by agent session …". An issue assigned in Linear without a session, or reassigned there, is claimed by its assignee's display name, so agents leave alone work a person took. Clearing a Claim unassigns the issue only if it is still assigned to the user jfl assigned, and removes the attachment once it keeps nothing else.
- **`blocked_by` Links** between the team's issues are Linear's blocking relations, so they show in Linear and a "blocked by" a teammate adds there is a Link jfl follows. Every other Link, and a Link to an Artifact kept elsewhere (a Spec in files), is kept in the metadata of the same attachment.
- **Comments** are issue comments.
- **Archived issues** are not listed, as in Linear's own views.

## Failures

| Linear answers | the Connector reports |
|---|---|
| no API key, `401`, `403`, or an `AUTHENTICATION_ERROR` or `FORBIDDEN` error | `auth` |
| `429`, or a `RATELIMITED` error | `rate_limit`, with `retry_after` from `Retry-After` or `X-RateLimit-Requests-Reset` |
| no answer, `502`, `503`, `504` | `network` |
| no issue with that number in the team, or one without the Type's label | `not_found` |
| any other GraphQL error, a team it can't see, a label or workflow state the team lacks, a blocker that isn't one of the Type's issues, or an `assignee` who isn't in the workspace | `invalid` |
| anything else | `internal` |

Creating an issue with Links, and a Claim, take several Linear requests; Linear has no transaction, so a failure part way leaves what was done before it, and jfl reports the tracker problem. The Connector looks up a new issue's blockers before creating it, so a missing blocker creates nothing.
