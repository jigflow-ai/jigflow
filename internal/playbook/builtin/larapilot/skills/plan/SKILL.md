---
description: Plan the Requirement in Focus as Stories, each broken into Tasks small enough to commit and review one at a time, proposed to the person as one unit.
changes: true
invocation: bound
guidelines: [conventions]
personas:
  - name: architect
    fallback: "Sees the system as a whole: its parts, the boundaries between them, and what each change costs the rest."
---
Plan the Requirement in Focus.

1. Read the Requirement's body, its **Done means**, and the PRD it is part of, in their files in .jigflow/state. If its priority is `wont`, don't plan it: propose `{move: <id>, to: dropped}` for the person to confirm, and stop.
2. Read the code the Requirement touches and the conventions Guideline.
3. Split the Requirement into Stories: slices a user can try on their own, together meeting every check under Done means. Give each Story acceptance criteria.
4. Split each Story into Tasks. A Task is one change, small enough to review in one sitting and commit as one commit, that leaves the tests passing. Say which Tasks must wait for others (blocked_by).
5. Propose the plan as one unit, with the Requirement's move to planned, in a Proposal file:

   ```yaml
   summary: plan REQ-3 as 2 Stories and 5 Tasks
   items:
     - {create: Story, ref: s1, title: A visitor signs up and gets a confirmation email, links: {implements: [REQ-3]}}
     - {create: Task, ref: t1, title: Users table with a unique email, links: {part_of: [s1]}}
     - {create: Task, title: Sign-up form and endpoint, links: {part_of: [s1], blocked_by: [t1]}}
     - {move: REQ-3, to: planned}
   ```

   Run `jfl propose <file>`, tell the person the Proposal's id, and wait for them to approve or reject it.
6. Once it is approved, write each Story's body (its acceptance criteria) and each Task's body (what to change, where, and how to know it works) in their files in .jigflow/state. If it is rejected, ask the person why and propose again.
