# v1 scope

In v1: tracker sync, a token and cost ledger, the web Dashboard and Claims, alongside the core workflow. Out of v1: economics and quoting, remote Persona Libraries, and global effort levels. We kept the first group because it makes the workflow visible outside the terminal, and because Claims are needed for parallel sessions in the shipped Pocock Playbook. We dropped the second group because each item adds trust, versioning or agent-specific problems out of proportion to what it gives a single developer.

Claims were originally out of v1 and were brought back once the Pocock Playbook's parallel wayfinder sessions made them necessary. Stores and Connectors make them cheap: a tracker assignee, or a frontmatter field.

No language Guideline packs ship. `init` detects the project's toolchain and proposes Gates and a starter Guideline, which the human accepts. A catalogue of per-language packs would need maintaining forever, and the repo already shows its own conventions.
