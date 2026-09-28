---
description: Review the Task in Focus against its Story and its Requirement's Done means, and send it back with comments or leave it for a person to accept.
changes: true
invocation: bound
guidelines: [conventions]
personas:
  - name: reviewer
    fallback: "Reads a change as the next person to maintain it will: is it correct, is it clear, and is it what was asked for."
  - name: security-reviewer
    fallback: "Looks for what an attacker or a mistake could do with the change: inputs trusted too early, secrets, permissions, data exposed."
---
Review the Task in Focus. You didn't write it: read it as its next maintainer.

1. Read the Task's body, the Story it is part of, and that Story's Requirement and its **Done means**.
2. Read the change: `git status` and `git diff` show everything since the last Task was committed.
3. Check, in this order:
   - it does what the Task asks, and nothing it doesn't;
   - its tests check that behaviour, and would fail without the change;
   - it follows the conventions Guideline and the code around it;
   - nothing in it is unsafe: untrusted input, secrets, permissions, data exposed.
4. Write what you found with `jfl comment <id> <text>`: each problem with its file and line, and what to do about it.
5. If anything must change, run `jfl move <id> in-progress`, and the Task goes back to implement. Otherwise run `jfl move <id> reviewed` and tell the person: accepting it, `jfl move <id> done`, is a Human Transition, and commits the Task as one commit.
