---
name: lead-mockup
description: Use when the user wants to explore visual layouts or build an interactive multi-file HTML mockup on the whiteboard via mockup-writer and wb:panel src.
---

<ws-preload playbook="lead-mockup">
# Mockup

Call in parallel:
- `wsflow/playbook.read(name: "lead-mockup", session_key: <your key, omit if fresh>)`
- `wsflow/workflow_manual(session_key: <your key or "obsidian-latch" if fresh>, root: <absolute worktree path if fresh>)`

After both return, execute the procedure returned by `wsflow/playbook.read`.
</ws-preload>
