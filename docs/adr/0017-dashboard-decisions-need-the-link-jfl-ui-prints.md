# Dashboard decisions need the link jfl ui prints, and run the person's commands

The Dashboard is the out-of-band channel for approvals and Human Transitions (ADR 0003): an agent in a terminal can't click in the person's browser. But an agent on the same machine can reach the Dashboard's address with curl as easily as the browser can, so the address alone can't tell a person's click from an agent's request. `jfl ui` therefore makes a random key each time it starts and prints it, in a link, only in the terminal of the person who started it. Opening the link makes their browser keep the key in a cookie (`HttpOnly`, `SameSite=Strict`, one per port) and drops it from the address bar. Only requests that carry it may approve or reject a Proposal or make a Transition; without it the pages still show the project, say how to get the key, and offer no forms, and a decision is refused with 403. A Dashboard started in an agent session (`JFL_SESSION` set) prints no key: it is only to look at.

A page elsewhere could make the person's browser post a form to the Dashboard, and the browser would send the cookie along if it weren't `SameSite=Strict`. On top of that, decisions go through Go's `http.CrossOriginProtection`, which refuses a request the browser says came from another site, and the Host check of ADR 0016 still refuses names other than loopback ones.

Each decision runs the same code as the command a person would type, as that person, with the click taking the place of the terminal's `y`: `jfl approve`, `jfl reject` and `jfl move` validate, run Gates and Actions, re-validate bodies edited outside jfl, write the Ledger and reach Connector Stores exactly as they do in a terminal, and the page shows what they said. Before approving, the person may edit a proposed creation's title, starting Status and Links; the edited Proposal is approved all or nothing, and kept as approved with the edits. The Dashboard offers a button for each Human Transition out of an Artifact's Status, and never a way to edit an Artifact's body. (Since #44 it also runs `jfl comment` for a person from an Artifact's page, under the same key: the one way it adds to a body, appending a comment signed as theirs, or adding it in the tracker. ADR 0030 partly supersedes this: under the same key, the Playbook page also changes the values of the Playbook file, each change a Proposal the person makes and approves in the same click.) Decisions are made one at a time; every page still reads the project afresh (ADR 0016).

A Transition declared `human: dashboard` is a Human Transition the Playbook requires making in the Dashboard: `jfl move` refuses it without asking, even in an interactive terminal, and `jfl approve` refuses a Proposal that makes it, which is then approved in the Dashboard.

## Considered Options

- Trusting any request from this machine: rejected, since an agent's curl is a request from this machine; the Dashboard would be no stronger than the terminal's confirmation.
- A key the person types, or a login: rejected as ceremony for a single-user local tool; the printed link gives the same guarantee with one click.
- A CSRF token in each form: not needed with a `SameSite=Strict` cookie and the browser's own report of where a request comes from; a token in the page would also be readable by anyone who can fetch the page.
- Offering every Transition in the Dashboard: rejected to keep it to decisions only a person may make; other moves stay with `jfl move`, although the Dashboard makes any declared Transition a person asks it for, as `jfl move` does.

## Consequences

- The key lasts until `jfl ui` stops, and a bookmark to the Dashboard shows the project but can't act after a restart: the person opens the new link.
- An agent that reads the person's terminal, their browser's cookies or starts its own Dashboard without `JFL_SESSION` can still act: Human Transitions stay a guardrail, not a security boundary (ADR 0003).
- Proposal items that move an Artifact aren't editable; a person rejects the Proposal instead.
