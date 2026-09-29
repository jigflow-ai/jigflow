# The Dashboard serves only this machine, and reads everything afresh

`jfl ui` serves the Dashboard from the same binary, with Go's standard library only (`net/http`, `html/template`, `embed`): its pages are rendered on the server, with no JavaScript and no build step. (ADR 0025 partly supersedes this: each page now carries a little inline JavaScript, still with no build step, that makes it update itself, and every page works without it.) It listens only on a loopback address (`127.0.0.1:7457` unless `--addr` names another), and answers only requests addressed to it by a loopback name (`127.0.0.1`, `::1` or `localhost`), refusing any other `Host` with 403. The Dashboard is a person's view of their project and, from the next ticket on, the out-of-band channel for approvals and Human Transitions (ADR 0003); a web page elsewhere could otherwise point a name of its own at 127.0.0.1 and read, or later act on, the Dashboard through the person's browser.

Every page loads the Playbook and lists the Stores again, so it shows what jfl last wrote, whoever wrote it; the Dashboard keeps no state of its own, and changes the project only by running a person's commands for them (ADR 0002, ADR 0017). A page that can't be read shows why, and a Connector failing is shown as a tracker problem, not a workflow refusal, with 502. Pages that don't need the tracker, such as the Status machines, still work while it is unreachable.

## Considered Options

- A JavaScript front end over a JSON API: rejected for v1, since it needs a build step and a second language for pages a person reloads; the server-rendered pages can grow forms for approvals without one.
- Serving on every interface, to open the Dashboard from another device: rejected, since the Dashboard is the channel an agent in a terminal can't reach, and a network can.
- Caching the Artifacts between requests: rejected, since a Dashboard that lags behind the CLI would mislead the person deciding on it.

## Consequences

- The Dashboard shows the backlog as a person sees it: why `next` skips agent work is given as it would be for a person, with no session's Claims counted as its own. How it tells a person's click from an agent's request, once it can act, is settled in ADR 0017.
- A tracker-kept backlog is listed from the tracker on every load of the backlog page.
