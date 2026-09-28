# The GitHub Connector keeps Claim sessions and extra Links in a hidden comment in the issue body

A Claim names an agent session, but a GitHub assignee must be a GitHub user, and parallel sessions usually share one token and so one user. The GitHub Issues Connector therefore assigns the issue to that user, so the Claim shows in GitHub, and keeps the session in an HTML comment at the end of the issue's body (`<!-- jigflow: {…} -->`), which GitHub doesn't render and the Connector leaves out of the body it reports. Links GitHub has no relation for, and `blocked_by` where a repository has no issue dependencies, go in the same comment. An assignee set in GitHub without a matching session is reported as the Claim of that login, so agents leave work a person took alone.

## Considered Options

- A label per session (`claimed: agent-…`): rejected because it would fill the repository with one-off labels.
- A comment on the issue: rejected because reading Claims back would take one more request per issue on every list.
- Reporting the assignee's login as the Claim: rejected because every session using the same token would share one Claim.
