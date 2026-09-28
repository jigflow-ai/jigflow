# The Linear Connector keeps Claim sessions and extra Links in an attachment of the issue

The Linear Connector has the same problem as the GitHub one (ADR 0012): a Claim names an agent session, a Linear assignee must be a Linear user, and parallel sessions usually share one API key and so one user. It assigns the issue to that user, so the Claim shows in Linear, and keeps the session in the metadata of an attachment with a fixed URL (`https://jigflow.invalid/meta`), which Linear keeps once per issue and shows in the issue's sidebar as "JigFlow — Claimed by agent session …". Links Linear has no relation for go in the same metadata; `blocked_by` Links are Linear's blocking relations. The attachment is read with the issue in the same GraphQL query, so reading Claims back costs no extra request, and an assignee set in Linear without a matching session is the Claim of that user's display name.

## Considered Options

- A hidden HTML comment at the end of the description, as the GitHub Connector does: rejected because Linear stores descriptions in its own document format and doesn't promise to keep raw HTML through the editor, so a teammate's edit could drop the Claim.
- A label per session: rejected, as for GitHub, because it would fill the workspace with one-off labels.
- Reporting the assignee's display name as the Claim: rejected because every session using the same API key would share one Claim.
