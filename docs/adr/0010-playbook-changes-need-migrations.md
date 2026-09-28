# Playbook changes that orphan Artifacts need a declared migration

A Playbook, or a Base Playbook update, that would leave existing Artifacts in an undeclared Status fails to load, and the error lists the orphans, until a Playbook Migration maps the old Statuses to new ones. We rejected quietly allowing orphans, because a workflow whose Artifacts can fall out of the Status machine can't be trusted by `next` or autopilot. `jfl check` validates every Playbook (reachability, final Statuses, missing Skills, Links and Personas) on load and in CI.
