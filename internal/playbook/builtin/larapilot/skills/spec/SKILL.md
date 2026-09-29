---
description: Specify the approved PRD in Focus as Requirements prioritised with MoSCoW, each saying what "Done means", proposed to the person as one unit.
changes: true
invocation: bound
personas:
  - name: product-owner
    fallback: "Speaks for the people the product is for: asks what problem it solves, for whom, and what they would give up first."
  - name: tester
    fallback: "Asks how anyone would know a thing works, and what would make it fail; turns vague wishes into checks a person can run."
---
Specify the PRD in Focus, which a person has approved, as Requirements.

1. Read the PRD's body. Every Journey step and every quality target must end up in a Requirement, or in the PRD's Out of scope.
2. Draft the Requirements. Each one:
   - asks for one thing the product must do or be, in a sentence, without saying how to build it;
   - has a MoSCoW priority: `must` (the product is useless without it), `should` (important, but it can ship without it), `could` (if time allows) or `wont` (agreed not to do this time);
   - says under **Done means** how a person can tell it is met: observable checks, each one pass or fail.
3. Add a **Requirements** section to the PRD's body listing each draft: its title, priority and Done means. This is what the person reads while deciding.
4. Propose them as one unit, with the PRD's move to specified, in a Proposal file:

   ```yaml
   summary: specify PRD-1 as 5 Requirements
   items:
     - {create: Requirement, ref: signup, title: A visitor can sign up with an email address, fields: {priority: must}, links: {part_of: [PRD-1]}}
     - {create: Requirement, title: ..., fields: {priority: should}, links: {part_of: [PRD-1]}}
     - {move: PRD-1, to: specified}
   ```

   Put it forward and ask the person for their Confirmation. If jfl's MCP tools include `approve`, use the `propose` tool on the file: it asks them at once, in a form only they see, and its result says what they decided. Otherwise run `jfl propose <file>`. While it still waits, tell them the Proposal's id and where: `jfl approve <id>` or `jfl reject <id>` in their own terminal, or the Dashboard (`jfl ui`). Wait for their decision.
5. Once it is approved, write each Requirement's body in its file in .jigflow/state: what it asks for, then **Done means** with its checks, as the PRD's Requirements section says. If it is rejected, ask the person why and propose again.
