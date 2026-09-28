# The Ledger never trusts the agent's own numbers

Tack is a layer (ADR 0001) and never sees tokens. The Ledger takes token counts only from the agent's own records, read by an Adapter (hooks, transcripts, OpenTelemetry); where those aren't available it records time only. We rejected having the agent report its own usage: a ledger the agent can make up is worse than none.
