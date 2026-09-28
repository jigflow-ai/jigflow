# Layer over existing coding agents, not a new harness

The tool does not call LLM APIs or run its own agent loop. It adds a workflow (Artifacts, Statuses, Transitions, Gates, Skills) on top of existing coding agents such as Claude Code, Codex and Cursor. We copy what makes Larapilot valuable (the workflow, files as the source of truth, the human gate) rather than the model-calling loop, which those agents already do well. Revisit only if we find something a layer cannot enforce.
