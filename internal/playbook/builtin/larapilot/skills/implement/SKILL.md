---
description: Implement the Task in Focus as one change that passes the project's tests and lint, then hand it to review.
changes: true
invocation: bound
guidelines: [conventions]
personas:
  - name: engineer
    fallback: "Makes the smallest change that does the job well, in the style of the code around it, and leaves the code easier to change than they found it."
  - name: tester
    fallback: "Asks how anyone would know a thing works, and what would make it fail; turns vague wishes into checks a person can run."
---
Implement the Task in Focus.

1. If it is in ready, run `jfl move <id> in-progress`. If it came back from review, read the review's comments at the end of its body first: they are what to fix, each under the Persona that found it.
2. Read the Task's body, the Story it is part of and that Story's Requirement, and the conventions Guideline. Read the Story's `## Technical plan` before changing any code, and build the Task the way it says: its design, the conventions it names, and how it says to test. If the plan is wrong for the code you find, don't work around it: say why with `jfl comment <id> <text>` and ask the person.
3. Write a failing test for what the Task asks, then the code that makes it pass, one behaviour at a time. Change only what the Task needs; note anything else you find with `jfl comment <id> <text>`.
4. Leave one comment per Persona you applied, on the Task in Focus, with `jfl comment <id> --persona <name> <text>`: as `--persona engineer`, what the change does and why it is built that way; as `--persona tester`, what the tests check and what they leave out. A Persona that found nothing says what it checked. If jfl refuses a Persona because it isn't usable in the project, say so in a plain `jfl comment <id> <text>`, without `--persona`: which Persona couldn't be applied.
5. Run `jfl move <id> in-review`. It runs the project's Gates, tests and lint, and refuses the move with their output if either fails: fix the code and move again. Never change a Gate to make it pass.
6. Don't commit: the commit is made when a person accepts the review.
