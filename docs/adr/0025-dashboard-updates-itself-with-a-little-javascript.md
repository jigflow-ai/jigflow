# The Dashboard updates itself, with a little JavaScript and no build step

(Its stream is superseded by ADR 0029: each page now asks `/changes` whether the project changed, every 2 seconds while its tab is visible, since a stream per tab ran into the browser's limit of 6 connections per host. The rest stands.)

A person keeps the Dashboard open while an agent works, waiting for a Proposal to approve or a Human Transition to make, and a page that shows the project only as of its last load makes them reload to find out. So the Dashboard now updates itself, partly superseding ADR 0016's "no JavaScript": each page carries a small inline script, with no build step, that listens to a server-sent event stream. While a page is open, `jfl ui` looks over `.jigflow/` about once a second (names, sizes and modification times, with the standard library only) and asks a tracker Store every 30 seconds, and says on the stream when something changed. The page then reloads, keeping its scroll position, unless the person has started editing a Proposal's form: then it shows a bar saying the project changed, and leaves their edits alone. A decision on something that changed meanwhile is refused as it already is, by the command the click runs. Every page does this, and none reloads only because time passed; the Ledger page says as of when its times are.

The pages are still rendered on the server, read the project afresh and keep no state (ADR 0016): with JavaScript off, every page and every decision works as before, and the person reloads by hand. The stream carries no content, only that something changed, so it needs no key (ADR 0017) and passes the same loopback Host check as every page, and a Dashboard started in an agent session, only to look at, updates too.

## Considered Options

- A `<meta http-equiv="refresh">` every few seconds, keeping "no JavaScript": rejected, since it would wipe a Proposal the person is editing before approving it, and lose their place on the page.
- Swapping in only the changed sections: rejected, as fragment endpoints and merging on the page, the front end ADR 0016 avoided, where reloading the page the server renders is enough.
- A JavaScript front end over a JSON API: still rejected, as in ADR 0016.
- A file watcher dependency such as `fsnotify`: rejected, since looking over a directory this size once a second costs little and keeps the Dashboard to the standard library.
- Asking the tracker as often as the files: rejected, since each ask counts against the tracker's rate limit; changes made through jfl land in files and show at once, and only an edit made in the tracker itself waits up to 30 seconds.
