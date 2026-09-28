# jfl, not each Connector, maps Statuses and marks agent-written text

ADR 0008 has Connectors map Playbook Statuses to a repo's labels and mark agent-written text as AI-generated. We moved both into jfl: the project settings map Statuses and field values to tracker labels or states, jfl translates them in both directions, and jfl appends the configurable marker to text an agent writes before sending it. The Connector protocol (docs/connector-protocol.md) is in tracker terms only, labels, states and assignees, so a Connector knows nothing of the Playbook, every Connector maps and marks the same way, and one written in a hurry can't forget the marker or misread a mapping. It also means the rules are tested once, in the CLI.

## Considered Options

- Sending the mapping and marker to the Connector and letting it apply them, as ADR 0008 first described: rejected because each Connector would reimplement the same rules, and the guarantee that agent text is marked would depend on every third-party Connector.
