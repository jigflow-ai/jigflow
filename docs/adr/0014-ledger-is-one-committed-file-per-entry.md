# The Ledger is one committed file per entry, timed by jfl's own clock

The Ledger is committed in `.jigflow/ledger`, whichever Store keeps the Artifacts, and every entry is a file of its own, named by when it was written and by a random id of the process that wrote it. An entry is a Status change (an Artifact created, moved through a Transition, created or moved by an approved Proposal, or migrated) or a change of an agent session's Focus. Entries are only ever added, so parallel sessions, on one machine or on branches merged later, never touch the same file and git never has a conflict to report. `jfl ledger` sums the entries: an Artifact's time in a Status runs from the change that moved it in to the one that moved it out, or to now while it waits there; an agent session's time runs from one Focus change to the next and is charged to the Artifact in Focus, or to unattributed. A session's Focus itself still lives only in the session; what is committed is when it changed, since that is what charges time.

Every time comes from jfl's clock when it writes the entry. Tests set that clock through an environment variable that only the tests' build reads (`-ldflags -X`), so a shipped jfl has no way for an agent to set the time it records (ADR 0007).

## Considered Options

- One file for the whole Ledger, or one per Artifact: rejected because two sessions appending to the same file on different branches conflict when merged.
- One append-only file per session: rejected because people's commands have no session id, so every person would share one file, and a resumed session on two machines would too.
- Charging the time after a session's last Focus change up to now: rejected because jfl can't tell a session still at work from one that ended, so that time isn't charged until the Focus next changes.
- Counting time in a final Status: rejected because no work waits there; time in one is counted only if the Artifact leaves it again.
- Recording Status changes made in a tracker outside jfl: out of reach, since only jfl's own Transitions pass through it (ADR 0002); the Ledger reads no tracker.
