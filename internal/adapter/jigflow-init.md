Set this repository up with JigFlow from chat: the person tells you how they want to work, and you run `jfl init` for them with every answer as a flag. The choices are the person's. You find the facts, suggest, and run `jfl init` only with answers the person has seen: the Playbook and Adapter they chose, and the settings and labels you showed them.

## 0. After a restart

If `.jigflow/playbook.yaml` exists and jfl's MCP tools include `approve`, the project was set up before this session started, so skip to step 6.

## 1. Look at the repository

Look at the repository before asking anything:

- Is it set up already? With `.jigflow/playbook.yaml`, `jfl init` keeps the Playbook chosen and asks only for what is missing: say so, and skip the choice of Playbook.
- Its languages, its test and lint commands, its CI, and how big it is.
- `git remote -v`: a GitHub remote gives the `owner/name` a GitHub Connector needs. Whether the team works in GitHub Issues (`gh issue list`, `gh label list`) or Linear.
- Docs of a product not built yet (a spec, a PRD, notes), or code already doing its job.

## 2. Suggest a Playbook

Explain the three choices in a few lines each, then suggest the one that fits what you found, and why:

- **Larapilot-style** (`--playbook larapilot`): a heavy-spec way of working, built into jfl. A PRD, written with `/inception` for a new product or `/adopt` for an existing codebase, is specified as Requirements, planned as Stories and Tasks, implemented test-first and reviewed in a fresh session. The person approves each step. Every Artifact is a Markdown file in the repository, so no tracker is needed.
- **Pocock** (`--playbook pocock`): Matt Pocock's engineering skills as a Playbook. Specs, Tickets and Issues live in GitHub Issues: `/to-spec` turns a conversation into a Spec, broken into Tickets that are implemented test-first and reviewed; `/triage` walks through filed Issues; `/wayfinder` charts an effort too big for one session. It needs a GitHub repository and a token in `GH_TOKEN` (with the GitHub CLI signed in, `GH_TOKEN=$(gh auth token)`).
- **Build your own** (`--playbook own`): the `/playbook-author` Skill interviews the person about how they work and proposes their own Artifact Types, Statuses and Skills, for a way of working neither fits.

There is no default: wait for the person to choose. When they leave it to you, pick the one you suggested and say so.

Publish for the agent you run in, `--adapter claude-code`, unless the person names another (`agents-md` for agents that read AGENTS.md). A project set up already keeps the Adapters it published through, so leave the flag out there unless they ask for another.

## 3. Work out the settings and labels

Pick a session id of your own, such as `init-` followed by the output of `date +%s`, and give it to every `jfl` command until the person restarts Claude Code, prefixing each one with `JFL_SESSION=<id>`, the same id every time. It tells jfl the command is yours, not the person's: the hooks that normally set it exist only after the restart.

Once the person has chosen the Playbook, run `jfl init` with the Playbook and the Adapter only:

```
JFL_SESSION=<id> jfl init --playbook <larapilot|pocock|own> --adapter claude-code
```

A Playbook that keeps Artifacts in files needs nothing more, and this sets the project up: go on to step 5. A Playbook that keeps them in a tracker fails with exit status 2, naming every flag still missing: a `--setting` for each Connector setting the Playbook leaves empty, and a `--label` for each Status of each Type it keeps there. It wrote nothing.

Work out each value you can:

- A GitHub `repo` setting is the `owner/name` of the git remote.
- A Status's label is the tracker's own label for it when it has one that fits (`gh label list`), or else the Status's name.
- Ask for the rest, such as a Linear `team`.

Show the person every setting and label you will use, as a list, and change the ones they correct. Every label goes in a flag: in an agent session nothing defaults.

## 4. Run jfl init

Give every answer as a flag, quoting a value with spaces:

```
JFL_SESSION=<id> jfl init --playbook pocock --adapter claude-code \
  --setting github.repo=acme/shop \
  --label Ticket.in-progress="status: doing" --label Ticket.done=done
```

A flag is `--setting <connector>.<setting>=<value>`, or `--setting <connector>.<Type>.<setting>=<value>` for a Type's own, and `--label <Type>.<status>=<label>`. When `jfl init` exits with status 2, an answer was missing: its error names the flags to add, and it wrote nothing. Work out or ask for what it names, and run it again with them. A Playbook in a repository jfl can't fetch, such as Pocock's before it is published, fails too: tell the person, who can point `JFL_POCOCK_GIT` at a checkout of it.

## 5. Read back what was set up

Read back to the person, from what `jfl init` printed:

- The Playbook, and the Adapter it published through, with the files it published.
- Each Connector's settings and the label of each Status.
- The toolchain it detected, and the Proposal it put forward: the Gate commands and the starter Guideline, one line each, with its id.

The Proposal is your session's, so it waits for the person's Confirmation: never approve it yourself. They can approve it now in their own terminal (`jfl approve <id>`) or the Dashboard (`jfl ui`), or after the restart in a form only they see.

Then ask them to restart Claude Code in a new session, not a resumed one: quit, and start `claude` again without `--resume` or `--continue`. The hooks and jfl's MCP server `jfl init` set up are live only in a new session. Tell them what to do there:

- Type `/jigflow-init` again, or ask for the waiting Proposal, and you will ask them about it in a form only they see.
- Having chosen to build their own Playbook, type `/playbook-author`, which interviews them and proposes it.

Stop here until they restart.

## 6. After the restart

Ask the person about each Proposal still pending (`.jigflow/proposals/`, with `status: pending`) with the `approve` tool, which shows them a form only they see; its result says what they decided. When they put the form off, tell them where it waits: `jfl approve <id>` or `jfl reject <id>` in their own terminal, or the Dashboard (`jfl ui`).

For a Playbook of their own (`.jigflow/playbook.yaml` without `extends:` and no Types), tell them to type `/playbook-author`. Otherwise tell them to type `/jigflow` to find the first piece of work.
