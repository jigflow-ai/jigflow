---
description: Draw the Mockups of the Artifact you name, usually a PRD or a Requirement, in the style you describe, and link them from its body.
changes: true
invocation: user
personas:
  - name: designer
    fallback: "Designs for the people who will use a screen: what they come to do, what they must see to do it, and every state the screen can be in."
---
Draw the Mockups of the Artifact the person names, usually a PRD or a Requirement. If they named none, ask them which one.

1. Read the Artifact with `jfl show <id>`, and what it is part of: a Requirement's PRD, a Story's Requirement. Read the screens the code already has, if any, so the Mockups fit them.
2. Run `jfl check` and find the line `Mockups in <dir>`: `<dir>` is the Mockup folder the Playbook declares, from the project root. With no such line, the Playbook declares no Mockup folder: tell the person to add a `mockups:` key to `.jigflow/playbook.yaml`, and stop.
3. Ask the person for the style they want: the look, colours and type, any product or design system to follow, and the screens or devices to draw for. Don't pick a style for them. If the Artifact already has Mockups, ask whether to keep their style.
4. Write the Mockups into `<dir>/<id>/`, the subfolder named after the Artifact's id, such as `.jigflow/mockups/REQ-1/`: one HTML page per screen or state, with its own styles and scripts in the page or next to it in that folder, and no file from outside it, since the Dashboard serves each Mockup sandboxed, from the folder alone. Show every state a person will meet, empty, error and loading as well as the usual one.
5. Add a `## Mockups` section to the end of the Artifact's body, in its file in .jigflow/state, or replace the one it has: a list linking each Mockup by its path from the project root, with a line on what it shows:

   ```markdown
   ## Mockups

   - [Checkout](.jigflow/mockups/REQ-1/checkout.html): the cart, the address and the card on one page
   - [Card declined](.jigflow/mockups/REQ-1/checkout-declined.html): the error a declined card shows
   ```

   A link is the path from the project root, not relative to the body's file: the Dashboard recognises no other.
6. Tell the person the Mockups are on the Artifact's page and the Design page of the Dashboard (`jfl ui`), and ask what to change. Revise them until they are happy.

Don't move the Artifact, and propose nothing: designing changes no Status, so the work goes on as it would without Mockups. `plan` reads them from the body when it plans the Requirement.
