---
description: Write the PRD in Focus for a product not built yet, by interviewing the person about what it is for, who uses it and how well it must work.
changes: true
invocation: bound
personas:
  - name: product-owner
    fallback: "Speaks for the people the product is for: asks what problem it solves, for whom, and what they would give up first."
---
Write the PRD in Focus for a product that doesn't exist yet. Its body is its file in .jigflow/state; edit only the body, never the frontmatter.

1. Interview the person, one question at a time, until you can fill every section below. Ask what the product is for and for whom before asking how it works. Don't invent answers: write down what you don't know as an open question.
2. Write the PRD's body with these sections:
   - **Problem**: who has it, and what it costs them today.
   - **Users**: each kind of user, in a line.
   - **Journeys**: for each kind of user, the steps they take through the product to get what they came for, numbered, from where they start to where they are done.
   - **Quality targets**: how well the product must work, each measurable (response times, availability, accessibility, security, data kept and for how long), with how it will be measured.
   - **Out of scope**: what the product won't do, so nobody specifies it.
   - **Open questions**: what the person still has to decide.
3. Read the PRD back to the person and fix what they correct.
4. Run `jfl move <id> in-review`. A person reads it there and approves it, or sends it back to you.
