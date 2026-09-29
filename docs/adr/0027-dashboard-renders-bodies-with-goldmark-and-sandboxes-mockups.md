# The Dashboard renders bodies with goldmark, and serves Mockups sandboxed from one declared folder

The Dashboard now gives each Artifact a page of its own, with its body, Links and history, and shows the Mockups its bodies reference. A body is Markdown, and those written by the Larapilot-style Playbook's Skills rely on tables and checklists, so the Dashboard renders them with `goldmark`, its first dependency outside Go's standard library (partly superseding ADR 0016), with raw HTML turned off: a body is text anyone, or any agent, edits, and must not be able to put a script on a page that carries decision forms.

Mockups are HTML with scripts of their own, so they are a sharper version of the same risk. The Playbook declares one folder for them, and the Dashboard serves only files inside it, each with `Content-Security-Policy: sandbox allow-scripts`: a Mockup runs, but in an opaque origin, so it can't read an open Dashboard page, its storage or the key in the link `jfl ui` prints (ADR 0017), and so can't approve a Proposal or make a Human Transition for the person.

## Considered Options

- A small Markdown renderer kept in the repository: rejected, since tables, task lists and code blocks already make it a project of its own, and `goldmark` has no dependencies.
- Showing bodies as preformatted text: rejected, since the point of the page is to read a Story or a plan as the Skill wrote it.
- Serving any repository file a body links to: rejected, since it would put every file, `.env` included, one link away from the browser, and leaves no one place to list Mockups from.
- A second loopback port for Mockups, a separate origin by construction: rejected as more to run and explain than a sandbox header that gives the same separation.

## Consequences

- A body's own HTML shows as text. A Skill that wants a picture on the page links a Mockup or an image in the folder.
- Something in the Mockup folder that needs same-origin access (cookies, storage shared with the Dashboard) doesn't get it; Mockups are pictures that move, not apps.
