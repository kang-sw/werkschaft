# External ended sessions hotfix

Restore pending-deletion lifecycle for non-hosted sessions that are ended or heartbeat-stale, retaining hosted runner-derived behavior and terminal handoff semantics. Update lifecycle, rail, conversation, and browser coverage as needed, including a hosted stopped fixture. Inspect stopped-state consumers and reconnect/retention paths for regressions. Run the requested Go build and tests, report browser sandbox limitations, and commit this plan with the implementation. Compatibility: expected T0; lifecycle is derived from existing registration fields.
