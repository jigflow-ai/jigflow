# Dashboard pages ask for changes instead of holding a stream

ADR 0025 made each Dashboard page hold a server-sent event stream to `/changes`, on which `jfl ui` said that the project changed. `jfl ui` serves over plain HTTP/1.1 on a loopback address (ADR 0016), and browsers allow 6 connections per host over HTTP/1.1: each open tab held one for as long as it was open, so a seventh Dashboard tab, or a click in any tab once six were open, waited for a connection that never freed.

So a page now asks instead. It asks `GET /changes?since=<version>` every 2 seconds while its tab is visible, and once as soon as the tab becomes visible again; a hidden tab doesn't ask. `/changes` answers at once: 204 when nothing changed since that version, 200 when something did, and the page then reloads, keeping its scroll position, or shows the bar when a form on it is being edited, as before. A request holds a connection only while it is answered, so any number of tabs stay usable. When `jfl ui` doesn't answer, the page keeps asking with the same version, and catches up on what changed meanwhile once it answers. A page stops asking once a decision is submitted, and every page still works without JavaScript.

`jfl ui` works out the version when asked, from the same looks as ADR 0025: the names, sizes and modification times under `.jigflow/` and in the Mockup folder, where the current branch and its commit are, and what each tracker Store lists, asked at most every 30 seconds. It looks at most once a second whatever the number of tabs, requests meanwhile sharing what it saw, and nothing runs between requests: with no page asking, it looks at nothing and asks no tracker. The stream and its watchers are gone. A request that still asks for `text/event-stream`, from a page shown before an upgrade, gets one `changed` event and the connection closes, so that the page reloads into one that asks.

`/changes` carries only whether something changed, never what, so it still needs no key (ADR 0017), and passes the same loopback Host check as every page.

## Considered Options

- One stream shared across tabs, with Web Locks electing the tab that holds it and a BroadcastChannel passing its events to the others: rejected, as the most JavaScript for the result, with a leader to hand over when its tab closes, and pages of different Dashboards on different ports to keep apart.
- A SharedWorker holding the one stream for every tab: rejected, as a second script to serve and keep in step with the pages, and not available in every browser the Dashboard is opened in.
- HTTP/2, which multiplexes many streams over one connection: rejected, since browsers speak it only over TLS, and a certificate for a loopback address is ceremony the local Dashboard shouldn't need.
- Long polling, a request held until something changes: rejected, since each tab still holds a connection while it waits, and the limit returns with six tabs waiting.
- Closing the stream in hidden tabs: rejected, since six visible windows still hit the limit, and a hidden tab that became visible had to open a stream and catch up anyway, which asking does more simply.

## Consequences

- A change shows within about 3 seconds of jfl writing it rather than about 1: up to 2 seconds until the page next asks, plus the second the version may be shared for.
- A page shown within a second of a change may carry a version from just before it, and reload once more than it needed to; it never misses a change, since the version is worked out before the page reads the project.
- A tab left visible asks every 2 seconds for as long as it is open, which costs `jfl ui` at most one look a second, however many tabs ask.
