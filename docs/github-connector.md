# GitHub Issues Connector

`jfl-connector-github` keeps the Artifacts of an Artifact Type as issues of a GitHub repository. It is the reference Connector: it ships alongside `jfl` (`make build` puts it in `bin/`), and it speaks the [Connector protocol](connector-protocol.md) like any other.

## Setting it up

Put `jfl-connector-github` on `PATH`, or give its path relative to the project root, and declare it in `.jigflow/playbook.yaml`:

```yaml
connectors:
  github:
    command: jfl-connector-github
    settings:
      repo: acme/shop        # owner/name, required
      create_labels: false   # true to create labels the repository lacks
    types:
      Ticket:
        settings: {label: ticket}
        statuses:
          in-progress: "status: doing"
          done: {state: closed}
```

| setting | |
|---|---|
| `repo` | the repository, `owner/name`. Required. Changing it, or `label`, re-points the Store: the Proposal says which Artifacts will no longer be seen. Remapping a Status or field value to another label or state relabels the issues carrying the old one instead (see [the protocol](connector-protocol.md#setting-up-a-connector)). |
| `label` | the label that tells this Artifact Type's issues apart, set per Type. The Connector lists and gets only issues with it, and adds it to the issues it creates. Without it, every issue of the repository is one of the Type's, so at most one Type can go without it. |
| `create_labels` | `true` creates a label the repository doesn't have when an issue needs it, with a grey colour you can change in GitHub. By default the Connector refuses the request instead (an `invalid` error naming the label), so a mistyped mapping doesn't fill the repository with labels. |
| `assignee` | the GitHub login a Claim assigns. By default, the user the token belongs to. |

The Connector [describes](connector-protocol.md#describing-the-settings) these settings without a token or the network, so the Dashboard's Playbook page shows each one, set or not, with `create_labels` as a toggle, and `jfl init` asks for `repo`, the one required.

The token comes from the environment jfl runs in, `GH_TOKEN` or else `GITHUB_TOKEN`, never from the Playbook file. It needs read and write access to the repository's issues (for a fine-grained token, the *Issues* permission). With the GitHub CLI signed in, `GH_TOKEN=$(gh auth token) jfl …` works. For a GitHub Enterprise Server, set `GITHUB_API_URL` to its REST API, e.g. `https://github.example.com/api/v3`; it is `https://api.github.com` otherwise.

## How Artifacts map to issues

- **Status and fields** are labels, or the open or closed state, as the project settings map them (jfl does the mapping, ADR 0011). A Status mapped to no state is open: moving an Artifact out of a closed Status reopens its issue.
- **Title and body** are the issue's. The body is the issue's to edit freely; the Connector keeps what it needs at its end, in a hidden HTML comment (`<!-- jigflow: … -->`), and leaves that comment out of the body it reports.
- **Claims** are assignees. A Claim assigns the issue to the `assignee` setting, or the token's user, and records the agent session in the hidden comment, since several sessions share one GitHub user. An issue assigned in GitHub without a session, or reassigned there, is claimed by its first assignee's login, so agents leave alone work a person took. Clearing a Claim removes only the assignee jfl added.
- **`blocked_by` Links** between issues of the repository are GitHub's native issue dependencies ("blocked by"), so they show in GitHub and a dependency a teammate adds in GitHub is a Link jfl follows. Where the repository has no issue dependencies (older GitHub Enterprise Servers), they are kept in the hidden comment instead. Every other Link, and a Link to an Artifact kept elsewhere (a Spec in files), is kept in the hidden comment.
- **Comments** are issue comments; `get` reports them, oldest first, for `jfl show`.
- **Pull requests** are never Artifacts, though GitHub lists them among issues.

## Failures

| GitHub answers | the Connector reports |
|---|---|
| no token, `401`, or `403` | `auth` |
| `403` or `429` with rate-limit headers | `rate_limit`, with `retry_after` from `Retry-After` or `X-RateLimit-Reset` |
| no answer, `502`, `503`, `504` | `network` |
| no issue by that number, a pull request, or an issue without the Type's label | `not_found` |
| any other `4xx`, including a repository it can't see, a label it lacks, or a state other than open or closed | `invalid` |
| anything else | `internal` |

A move or a create is several GitHub requests; GitHub has no transaction, so a failure part way leaves what was done before it, and jfl reports the tracker problem.
