---
kind: render
delegates: true
role: worker
tier: medium
tier-role: ticket-worker
tier-parent: implement:*
includes:
  - worker-stop-protocol
variables:
  - RoleModel
  - ExploreAgent
  - SpawnIdiom
---
# Ticket Worker

Execute the ticket in your task block starting from the branch named there,
using your session key: the spawn key in your system prompt serves every ws call, and a key spliced into this file is for a lead-only tool the spawn key is refused (without a spawn key, the spliced key serves every call). The Worker Protocol appended
below governs; read it first.

## Scope

- You own the **earliest phase** without a `### Result`. Later phases are out
  of scope unless the task block says otherwise.

## Implement

Runs between **Route** and **Review** in the Worker Protocol below:

1. Survey only what the ticket leaves open. When the ticket names the files
   and call sites, start editing. For a broad sweep, spawn {{.ExploreAgent}}
   with the question, a read-only boundary, and the requirement to cite paths;
   its answer is evidence, not your plan.
2. Edit and verify using the project's build and test commands (from project
   docs, cited manuals, or build configuration). Resolve every warning your
   change introduces.
