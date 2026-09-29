---
description: Write the PRD in Focus for a codebase that already exists, from what the code does today and what the person wants it to do next.
changes: true
invocation: bound
guidelines: [conventions]
personas:
  - name: architect
    fallback: "Sees the system as a whole: its parts, the boundaries between them, and what each change costs the rest."
  - name: product-owner
    fallback: "Speaks for the people the product is for: asks what problem it solves, for whom, and what they would give up first."
---
Write the PRD in Focus for a codebase that already exists. Its body is its file in .jigflow/state; edit only the body, never the frontmatter.

1. Read the project before asking anything: its README and documentation, how it is built and tested, its entry points, and the parts the code falls into. Read the conventions Guideline.
2. Write down what the product does today, as its users see it, and check it with the person.
3. Interview the person, one question at a time, about what should change: what is missing, what is wrong, and what must not break.
4. Write the PRD's body with these sections:
   - **Problem**: what the change is for, and who has the problem.
   - **Today**: what the product does now, the parts of the codebase that matter here, and its constraints (languages, frameworks, services, data).
   - **Users**: each kind of user, in a line.
   - **Journeys**: for each kind of user, the steps they take through the product once the change is made, numbered; mark the steps that change.
   - **Quality targets**: how well the product must work, each measurable, including what it already achieves and must keep.
   - **Out of scope**: what the change won't touch.
   - **Open questions**: what the person still has to decide.
5. Read the PRD back to the person and fix what they correct.
6. Leave one comment per Persona you applied, on the PRD in Focus, with `jfl comment <id> --persona <name> <text>`: as `--persona architect`, what in the codebase the change must respect and what it will cost the rest; as `--persona product-owner`, what the person wants changed, for whom, and what they would give up first. A Persona that found nothing says what it checked. If jfl refuses a Persona because it isn't usable in the project, say so in a plain `jfl comment <id> <text>`, without `--persona`: which Persona couldn't be applied.
7. Run `jfl move <id> in-review`. Approving it, the Human Transition to `approved`, is the person's: ask them for it. If jfl's MCP tools include `approve`, use the `move` tool to move it to `approved`: it asks them in a form only they see, and its result says whether they made or refused it. Otherwise tell them it waits for them: `jfl move <id> approved` in their own terminal, or the Dashboard (`jfl ui`). If they refuse it, ask what to change.
