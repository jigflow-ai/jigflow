# The CLI is the only writer of workflow state

Agents never change Statuses or Artifact frontmatter directly; they ask the CLI, which validates the Transition, checks its Guards, runs its Gates, and refuses Human Transitions. Without this rule, user-defined workflows would just be a folder of prompts the agent could ignore. This constraint holds no matter how much of the workflow the user customises.

Artifact bodies are the exception: humans and agents may edit them directly, so the files stay usable in any editor. The CLI detects edits it didn't make and re-validates the Artifact before allowing any Transition.
