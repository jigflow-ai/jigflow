# Connectors describe their settings

The Dashboard edits a Connector's settings in a form (ADR 0030), but jfl never knew which settings a Connector has: they are passed to it as they are (ADR 0008). A setting absent from the Playbook file, such as the GitHub Connector's yes/no `create_labels`, could not be shown at all. So the protocol gains an optional operation, `describe`. It asks for no Store and needs no token. The Connector answers with each setting's name, its kind (text, yes/no or number), whether it is required, whether it is given per Artifact Type, and a line of help. The form draws a field for each setting, a yes/no one as a toggle, and `jfl init` asks for required settings from the same answer. A Connector that doesn't know `describe` answers it as an unknown operation. The form then shows the settings present in the file, a `true` or `false` one as a toggle, plus a free row for any other, and the protocol stays at version 1.

## Considered Options

- Inferring settings only from the Playbook file: kept as the fallback, but it hides every setting nobody has written yet.
- A schema file shipped next to each Connector: rejected, as a second thing to install and keep in step with the executable, which is written in any language and is the only thing that knows its settings.
