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

1. Read the Requirement's body, its **Done means**, and the PRD it is part of, in their files in .jigflow/state. If its priority is `wont`, don't plan it: put forward a Proposal of `{move: <id>, to: dropped}` for the person to confirm, as step 5 does, and stop.
2. Read the code the Requirement touches and the conventions Guideline. Open the Mockups linked from the `## Mockups` sections of the Requirement and the PRD, if they have any: the screens the design shows, which the Stories and Tasks follow.
3. Split the Requirement into Stories: slices a user can try on their own, together meeting every check under Done means. Give each Story acceptance criteria, and note which Mockups it needs.
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

   Put it forward and ask the person for their Confirmation. If jfl's MCP tools include `approve`, use the `propose` tool on the file: it asks them at once, in a form only they see, and its result says what they decided. Otherwise run `jfl propose <file>`. While it still waits, tell them the Proposal's id and where: `jfl approve <id>` or `jfl reject <id>` in their own terminal, or the Dashboard (`jfl ui`). Wait for their decision.
6. Once it is approved, write each Story's body and each Task's body in their files in .jigflow/state. If it is rejected, ask the person why and propose again.

   A Story's body holds its acceptance criteria, then a `## Technical plan` section saying how it will be built:

   - **Context**: the code it touches and what is there today;
   - **Design**: the parts it adds or changes and how they fit together;
   - **Conventions**: the conventions Guideline's rules and the patterns in the code it follows;
   - **Testing**: how it will be tested, and at which boundary.

   When the Requirement or the PRD links Mockups, end each Story's body with a `## Mockups` section listing the Mockups that Story needs, linked by the same path the Requirement or PRD links them by, from the project root, such as `.jigflow/mockups/REQ-3/sign-up.html`. A Story that needs none has no such section.

   The approved Proposal is the person's approval of the plan: writing it needs no further Confirmation. A Task's body says what to change, where, and how to know it works, the way its Story's technical plan says.
