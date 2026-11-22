---
title: "감사 세그먼트 링 저장소: 저장 전 마스킹과 보존 정책"
parent: 261119-epic-ws-conversation-audit
blocked-by: 261119-feat-ws-audit-policy-event-envelope
related:
  261119-feat-ws-audit-capture-harness-adapters: "producer: writes envelopes into this store; may proceed in parallel"
  261119-feat-ws-audit-query-api: "consumer: reads this store"
  261112-feat-ws-mixed-version-compat-guards: "precedent for refusing writes to newer store formats"
  261114-feat-ws-conversation-audit-event-store: "superseded source of (594) rationale, (596), (597) and the file-mode/non-blocking constraints"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: bbbb91eecd66f6db
sage-review-completeness-reviewed: bbbb91eecd66f6db
completed: 2026-11-22
---

# Audit segment ring, redaction and retention

## Background

Third child of `261119-epic-ws-conversation-audit` ((1331)=a, 2026-11-19).
It persists the envelopes child 2 captures, redacts them before they touch
disk, and bounds how long and how much is kept.

Owner decision (1960)=a (2026-11-21) supersedes the SQLite choice in
(1331)=a. `ai-docs/manuals/performance.md` `## Storage Types` classifies
audit events as Type 1 append-only logs. The superseded ticket's (594)
supplies the rationale for keeping a dedicated audit store beside the
display ring.

Verified findings (codex.large read-only research, 2026-11-19):

- The display ring is intentionally lossy and unusable as an audit store.
- Redaction is regex best-effort: `wsredact` documents that a secret in an
  unrecognized shape passes through (`agents-plugin-tool/internal/wsredact/redact.go:1-9`).

## Decisions

1. **A dedicated Type 1 audit ring ((594) rationale, (1960)=a).** The
   display ring is a capped UI view whose trimming would discard audit data.
   Full tool outputs would bloat every history page read. The audit writer
   appends envelopes to the newest segment of a separate per-session ring.
   An in-memory index supports reads after recovery from the segments.
   - Rejected: the SQLite choice in (1331)=a. Under (1960)=a, audit events
     are append-only replay and range data. Text search scans the ring index.
     SQLite is for storage types 2-4, including structured history queried
     by field over the whole store.
2. **Redact before persist ((596), carried).** Before writing, `wsredact`
   runs over `in`, `out` and `raw`. A redaction keeps the rule name and the
   original length, never the original (`redactions: [{field, offset, rule,
   length}]`). This extends `wsredact` with a match-reporting entry point and
   keeps the `Secrets(s) string` signature for existing callers. Rejected:
   storing unredacted data in a separate secure store, which is a new
   key-management surface. What else secret handling needs is settled in
   Decision 7 ((1734)=a): nothing more. Redaction runs on the full body
   before any per-body cap (Decision 9), so a secret straddling the cut cannot
   be split into an unredacted fragment.
3. **Byte caps ((597), (1970)=a).** A per-session byte cap (config knob,
   default 256 MiB) drops the oldest whole segment first, because one runaway
   tool output could fill the disk within any time window. (597)'s time rule
   is settled in Decision 6 ((1733)=d). The per-store total byte cap is a
   separate config knob, default 4 GiB ((2010)=a, owner 2026-11-21). It
   evicts whole sessions' audit rings in least-recently-used order. Segment
   size is an implementation choice.
   - Rejected: b, 1 GiB, and c, 8 GiB. The 4 GiB default keeps more recent
     sessions than 1 GiB with half the maximum disk use of 8 GiB.
   - Cap edge cases ((2127), owner 2026-11-22): the per-session cap has a
     floor of four segments, and a segment is at most a quarter of the
     per-session cap, so one capped event (at most three 1 MiB bodies plus
     metadata, Decision 9) always fits. Store-wide LRU eviction removes the
     active (currently written) session last; an active session alone over
     the total cap drops its own oldest segments down to the cap, always
     keeping its newest segment. Rejected: dropping new events, or evicting
     the live ring whole. Both lose the most recent audit data, which the
     owner is most likely to inspect.
   - Limits hold in encoded record bytes, and the four byte knobs are
     validated as a set ((2129)=a, owner 2026-11-22). JSON can encode a
     1 MiB body of NUL bytes as 6 MiB (`wsaudit/envelope.go:376`), so a rule
     on raw body bytes alone cannot guarantee fit. (1) The per-body cap
     (Decision 9) measures the body's encoded bytes, cutting head and tail so
     each half fits its encoded share. (2) Everything outside the three bodies
     is bounded at 64 KiB encoded; redaction entries inside an omitted middle
     collapse to one summary entry per rule (count and total length) when
     they would exceed it. (3) The effective per-session cap is
     min(per-session, store total), with a 4 MiB floor; the per-body cap is
     at most 1/16 of it and a segment at most 1/4 of it, so one complete
     encoded record (three bodies, 64 KiB, framing) always fits a segment and
     the newest segment always fits the store total. (4) A config write that
     breaks the set is refused; a loaded file that breaks it is clamped to
     the nearest valid values with a logged warning. Rejected: accepting any
     values with oversized segments, which turns every cap into a soft
     target.
   - Metadata overflow ((2131)=a, owner 2026-11-22). When redaction entries
     and other envelope metadata would exceed the 64 KiB bound, even for a
     body under its cap with no omitted middle, entries are kept in offset
     order up to the bound and the rest collapse to one summary entry per
     (field, rule) with count and total original length. The stored body
     text stays masked and unchanged; only the overflow loses per-span
     offsets in the report. Rejected: cutting the record's bodies to
     previews so every redaction entry survives, which discards audit
     content to keep a report.
   - Identifier normalization ((2138), owner 2026-11-22). Before any
     metadata budget check, every identifier field (`id`, `session`,
     `harness`, `subkind`, `turn`, `call_id`, `parent_id`, `agent_id`)
     passes through one shared wsaudit function: a raw value of at most 256
     encoded bytes that does not begin with `sha256:` is kept verbatim;
     any other raw value (over 256 encoded bytes, or itself beginning with
     `sha256:`) becomes `sha256:<hex>` of its raw bytes. The kept and
     digested forms are disjoint, so a literal harness ID spelled like a
     digest never equals another call's key. The function is applied once,
     to raw harness input, by the store's ingestion and by every adapter's
     ring call ID (`ToolCallArgs.id`); audit lookup takes the normalized
     key a history row carries and never re-normalizes it. It runs whatever
     the metadata size, so a call and its result always normalize the same
     way. Rejected: prefix truncation (a history row's raw ID misses its
     stored event and distinct calls sharing a prefix merge; the agy mapper
     passes `toolCallId` through unbounded, `wsagent/agy.go:1341,1446`);
     exempting identifiers from the bound (the bound stops holding);
     normalizing only when metadata overflows (a long ID in a small
     envelope stays raw in the store while history carries its digest);
     digesting only long IDs (a literal `sha256:<hex>` ID collides with the
     digest of a different long ID).
   - Non-redaction metadata overflow ((2138), owner 2026-11-22). When
     metadata outside the three bodies still exceeds the 64 KiB bound after
     the redaction summary and identifier normalization: (1) required
     `meta` keys the readers depend on (`estimated`, `wsaudit/cut.go:34`)
     are reserved first; (2) the remaining optional `meta` entries are kept
     in key order up to the bound and the dropped keys' count and encoded
     bytes go into one truncation marker. The envelope's only free-form
     metadata is `meta map[string]any` and the identifier strings
     (`wsaudit/envelope.go:190-211`). Rejected: rejecting or splitting the
     record (a hole in the audit; readers must reassemble one event).
4. **Compatibility tier T0, with its own schema version.** A new store with no
   older reader. The on-disk ring format carries its schema version. A daemon
   refuses to open a newer version for writing, in line with
   `261112-feat-ws-mixed-version-compat-guards`.
5. **Memory-first ring discipline ((1960)=a).** Follow
   `ai-docs/manuals/performance.md`. Recover the index from retained segments,
   keep the current append handle open across writes, and serve reads from
   memory. Rotation opens the next segment. Retention removes whole segments
   or a whole session's audit ring, according to Decisions 3, 6 and 8.
   - Each envelope carries a source identity: holder generation and ring
     sequence for a sequenced line; holder generation, anchor and interval
     index for a tee line; or a deterministic key for a daemon-synthesized
     event. Include an event ordinal when one source yields multiple envelopes.
     On open, recover the highest retained envelope `seq` and last retained
     source identity per source, expose them to capture by extending the
     recovery contract alongside `Sink.Append`
     (agents-plugin-tool/internal/wsaudit/sink.go#L5-L13), and skip an append
     whose source identity is already retained. Capture resumes above the
     recovered sequence; holder reattachment replays records
     (agents-plugin-tool/internal/wsagent/holder.go#L253-L262,
     agents-plugin-tool/internal/wsagent/holder.go#L306-L320).
   - The audit index records a per-source finalized boundary: the highest
     source sequence the writer finalized, whether kept or dropped. Replay
     appends only source records above that boundary. A record at or below it
     without a retained identity, including one dropped on write error, counts
     as loss and is never appended late.
6. **Retention is a config knob, default 7 days after the session was last
   seen ((1733)=d, owner 2026-11-19).** The default is (597)'s approved
   behaviour, so the existing session prune semantics apply unchanged. An
   owner who audits older work lengthens the knob without a code change.
   Time retention ((1970)=a) removes a session's whole audit ring
   (Decision 8). Both byte caps apply independently (Decision 3).
   - Rejected: a, a fixed 7 days with no knob. It gives an owner who audits
     older work no recourse short of a code change.
   - Rejected: b, a fixed age per event (for example 30 days) independent of
     session activity. It changes (597)'s approved semantics and leaves a live
     session's older events pruned out from under it.
   - Rejected: c, no time limit with only the byte cap and an explicit
     session delete removing data. It keeps a long-lived store of
     best-effort-redacted data indefinitely (Decision 7).
   - The audit rings live under their own store root keyed by session ID,
     not inside the session directory, so the registry's seven-day session
     directory prune (`PruneRegistrations`,
     agents-plugin-tool/internal/wsdashboard/registry.go#L369-L425) does not
     cut a longer audit knob short. The audit store records each session's
     last-seen time in its own index and ages rings by this knob. An explicit
     session deletion still removes the session's audit ring (Decision 8).
     Use registration, end, existing heartbeat evidence and latest captured
     activity for last-seen. Preserve the final last-seen timestamp in the
     audit index before the registry removes the registration, and capture it
     at existing write points such as prune or unregister and daemon start
     from surviving registrations; add no audit heartbeat writes. Registry
     last-seen includes `HeartbeatAt`
     (agents-plugin-tool/internal/wsdashboard/registry.go#L246-L254), which
     the runner refreshes (agents-plugin-tool/internal/wsagent/runner_conv.go#L675-L679,
     agents-plugin-tool/internal/wsagent/runner_conv.go#L888-L914).
     Rejected: keeping the ring inside the session directory and capping the
     knob at the session prune age. It removes the owner's recourse this
     decision exists for.
   - A knob shorter than the session's own retention can leave a live session
     with no audit data. That is a valid state, not an error (the query API
     child serves it as empty) (pending 261119-feat-ws-audit-query-api).
7. **Secrets: best-effort redaction as it is, residual risk documented
   ((1734)=a, owner 2026-11-19).** (596) stands unchanged: redact before
   persist, keep rule name and original length only. `wsredact` is regex
   best-effort, so a secret in an unrecognized shape is stored in plain text.
   The feature's user documentation states this residual risk and explains
   how the byte caps, time retention and session deletion remove data. A
   shorter retention knob is therefore the owner's mitigation. It also states
   that a session deleted by an older daemon keeps its audit ring until the
   retention knob expires (Decision 8, (2126)=a). The
   documentation's location is chosen at implementation.
   - Rejected: b, a plus a size-only placeholder for calls that read known
     secret locations. A denylist misses as much as the regex does while
     hiding legitimate audit content.
   - Rejected: c, a plus encrypting audit data at rest with an OS-keychain
     key. It is the key-management surface (596) already rejected.
   - Rejected: d, a plus an owner action to purge one event or one session's
     audit data (the lead recommended a+d; the owner chose a only). No purge
     route, tool or UI control is added anywhere in the audit epic.
8. **One audit ring per session, deleted with the session ((1735)=a,
   (1960)=a).** Every approved read route is per session (`/s/<id>/audit/...`,
   (599)), so each lead and `ws/agent.*` child ((1731)=b) gets its own
   segment ring and index. Session deletion, time retention and store-wide
   eviction remove all its audit segments and index entries. Store-wide
   eviction removes audit data only. Reads and search remain per session.
   - Rejected: b, one shared audit database with a session column. It pays
     off only for cross-session search, which no approved contract needs, and
     lets one runaway session bloat the others.
   - Rejected: c, an audit table inside a daemon database shared with other
     SQLite-migrated stores. It couples audit retention and deletion to
     unrelated stores.
   - Orphaned rings after a downgrade ((2126)=a, owner 2026-11-22). An
     older daemon can remove a session directory (explicit delete,
     `wsdashboard/railworktreedelete.go:289`, `wsdashboard/index.go:500`, or
     idle prune, `wsdashboard/registry.go:419`) without leaving a reason the
     new store can read. On re-upgrade the store keeps such an orphaned ring
     and ages it by the Decision 6 knob from its recorded last-seen time. With
     the default 7 days this matches today's prune timing; only a lengthened
     knob keeps explicitly deleted data longer, and Decision 7's user
     documentation names that residual.
     Rejected: b, dropping every orphan at once. It cuts a lengthened
     retention short after any downgrade.
9. **Tool-result bodies: a per-body cap keeping head and tail ((1737)=b,
   owner 2026-11-19).** A per-body cap (config knob) bounds each stored `in`,
   `out` and `raw`. A body over the cap is stored as its head and tail with
   an explicit stored-truncation marker (the envelope's `stored_trunc`). The
   read API's range follow-up works within what is stored. This keeps the
   seen-versus-stored comparison the mockup approved while one runaway output
   can no longer evict a session's history.
   - Order: redact the full body, then apply the cap (Decision 2). Redaction
     `offset` values refer to the redacted body before the cap. A redaction
     inside the omitted middle stays listed with its rule and length but has
     nothing to render. The landed envelope fixes offsets as byte offsets in
     the redacted body before the cap (agents-plugin-tool/internal/wsaudit/envelope.go#L132-L142).
   - Carry the original model-seen byte boundary through the redaction and
     truncation mapping into the stored body, including when the boundary
     crosses a masked span. Matches can change length: `wsredact` replaces
     each secret span with the three-byte mask `***`
     (agents-plugin-tool/internal/wsredact/redact.go#L17-L18,
     agents-plugin-tool/internal/wsredact/redact.go#L117-L120).
   - Default cap 1 MiB per body in encoded bytes (Decision 3, (2129)=a), split evenly: the first 512 KiB and the last
     512 KiB. Measured 2026-11-22 over 66,126 tool results in 300 recent
     local Claude Code transcripts: median 329 B, p99 243 KB, max 682 KB, so
     no observed body reaches the cap, while the 256 MiB per-session cap
     still holds at least 256 capped bodies. An even split keeps the start of
     an output and its final error or summary lines equally.
     Rejected: 256 KiB, which would truncate about 1% of observed bodies and
     a fifth of their bytes; a head-heavy split, which loses trailing
     failures first.
   - Search covers stored text only. The omitted middle is not searchable.
   - Rejected: a, the full body bounded only by the per-session byte cap. One
     huge output can evict most of a session's history.
   - Rejected: c, only what the model saw (`seen` bytes) plus the full size.
     It drops the comparison with the part below the model's cut.
   - Rejected: d, previews only, as the display ring does today. It drops the
     comparison and defeats the audit's purpose.

## Constraints

- Implementation conventions (AGENTS.md): `agents-plugin-tool/` ->
  `ai-docs/manuals/shipped-surface-boundary.md`; `agents-plugin-tool/internal/`
  -> `ai-docs/manuals/release-compatibility.md`.
- Follow `ai-docs/manuals/performance.md` for Type 1 storage, memory-first
  interactive paths and measured before/after file ops.
- Config keys (Decisions 3, 6, 9): the per-session byte cap, per-store total
  byte cap, retention period and per-body cap are new config keys. They are
  additive, T0 (`ai-docs/manuals/release-compatibility.md`). An older binary
  that does not know them keeps running on its built-in behaviour. A new key is not a
  manifest field change. The manifest parser refuses unknown fields and
  `default_scope` values outside `session|project|global` (scenario 11, T3),
  so the keys use only existing fields and scopes. Wherever a key is declared
  in a config manifest, the `ws` and `wsflow` manifests must stay
  key-identical with only `default` differing, including the native-pi
  first-party and dashboard manifest copies
  (`TestShippedClaudeManifestsDifferOnlyInDefaults` checks the Claude manifest pair:
  agents-plugin-tool/internal/mcp/config_manifest_parity_test.go#L47-L94;
  `ai-docs/manuals/wsflow-mirroring.md`). Key names are chosen at
  implementation under the existing config registry naming.
- Writing must not block capture. Append off the hot path; on a write error,
  log and drop the event rather than stall the session (carried).
- Store files use mode 0600 and directories 0700, like the display ring (carried).
- Release-gate file-touch rule (parent epic): append not rewrite, no
  heartbeat writes, no per-event O(N) rereads.

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 69cb12676 (2026-11-22, commit): "Placed the shared audit envelope, model-seen cut, capture policy gate, and sink interface in a dependency-free internal/wsaudit package" — bearing: constrains
- 261119-epic-ws-conversation-audit (2026-11-19, Cross-Child Decisions): "Each session has its own append-only segment ring with an in-memory index. Session deletion removes its ring." — bearing: supports
- 261114-feat-ws-conversation-audit-event-store (2026-11-14, Decisions): "The audit store is a separate append-only per-session log" — bearing: supports
- 3a5ed2e71 (2026-11-21, commit): "Owner decisions (1960)=a and (1970)=a put audit on a Type 1 segment ring with a per-session cap, a store-wide LRU cap, and time retention." — bearing: supports
- 261118-feat-ws-display-transcript-memory-first (2026-11-19, Result): "Conversation rings append one JSONL line" — bearing: supports
- 54b93bfb (2026-11-12, commit): "optional ToolBatch.callArgs in the ring" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsredact/redact.go and new store, config and manifest work in Phases 1 and 2 |
| scope.surface | cross-module | store implements wsaudit.Sink and supplies the query child |
| scope.new_public_symbol | yes | match-reporting entry point extends wsredact across package boundaries |
| scope.new_type_contract | yes | match reporting and store read contract; exact signatures are unspecified |
| scope.test_surface | unknown | Phase verification requires tests but does not name test files |
| complexity.reuse_points | confirmed | wsredact.Secrets and wsaudit.Sink are present in agents-plugin-tool/internal/wsredact/redact.go and agents-plugin-tool/internal/wsaudit/sink.go |
| complexity.side_effect_risk | high | writes and evicts sensitive per-session audit data |
| risk.correctness | high | redaction, recovery, truncation and independent retention caps must compose correctly |
| risk.fit | high | producer sink, session lifecycle, configuration and query consumer must agree |
| risk.test | high | asynchronous failures, recovery, eviction and secret-boundary cases require coverage |
| risk.security_or_contract | high | best-effort redaction precedes durable storage of sensitive bodies |

## Phases

### Phase 1: Store, writer and redaction

After the envelope child lands, implement the per-session segment ring
(Decision 8) with its schema version, recovery, in-memory index and held-open
append handle. Implement the non-blocking writer behind the envelope child's
sink interface, redaction with match reporting (Decision 2) applied before every
write, and the per-body cap with the stored-truncation marker (Decision 9)
applied after it. Register the per-body cap, retention and both byte-cap config
keys (Constraints, "Config keys").
Recover and expose the sequence and source identities, and deduplicate replayed
identities using the per-source finalized boundary (Decision 5). Carry
model-seen boundaries through masking and truncation (Decision 9).

Verification: redaction tests (rule name and length kept, original absent
from persisted segments), including a secret straddling the per-body cut.
For an under-cap body with thousands of masked matches, verify the masked
body stays unchanged, redaction entries remain in offset order up to the
64 KiB metadata bound, and the rest collapse into one summary per (field,
rule) with count and total original length; encoded metadata stays within
the bound (Decision 3, (2131)=a). Verify a 300-byte `call_id` is stored
in the same shared digest form in a small envelope and in one whose
metadata overflows, a history row carrying its normalized key opens its
event, two long IDs sharing a prefix stay distinct, and a short literal ID
spelled as another long ID's `sha256:<hex>` key is stored under a
different key so both calls resolve independently in the same session
(Decision 3, identifier normalization). For a record whose non-redaction
metadata alone exceeds the bound, verify `meta.estimated` survives,
optional `meta` entries are kept in key order up to the remaining bound,
one truncation marker records the dropped keys' count and encoded bytes,
the event and its bodies are stored, and encoded metadata stays within the
bound (Decision 3, (2138)).
Verify a model-seen boundary crossing a masked span. Append, restart the
daemon, replay holder records, and append again against the recovered ring;
verify sequence continuity and no duplicate retained source identities.
Fail the append of source 10, successfully append 11, restart and replay:
10 is not appended after 11, loss count is 1, and the compaction cut remains
unchanged (agents-plugin-tool/internal/wsaudit/cut.go#L27-L45).
Refuse writes to a newer ring format, recover retained events into the
in-memory index, and verify independent session rings, append-handle reuse,
and file and directory modes. Verify that an oversized body keeps its head,
tail and marker and an under-cap body stays whole. A failing or slow writer
must drop and log without delaying the producer. Measure file ops per appended
event before/after per `performance.md`.
Verify a NUL-heavy body, including JSON escape expansion, fits each encoded
head and tail share. At the smallest valid configuration (4 MiB effective
per-session cap), verify one maximal encoded record with three capped bodies,
64 KiB of metadata and framing fits a segment. Verify a config write that
breaks the knob set is refused and a loaded file that breaks it is clamped
with a logged warning (Decision 3, (2129)=a).

### Result (12ce5478f) - 2026-11-22

Landed (commits 2b7bee20c, 72c888765, d05aba26b, fdb01530e, 12ce5478f on
`impl/develop/silk-dairy-cane`):

- `wsredact.Redact` returns per-match entries (rule, offset, original length)
  and `MapOffset`; the legacy text-only entry point is unchanged.
- New `internal/wsauditstore`: a per-session append-only segment ring with a
  schema-versioned header snapshot line per segment, held append handle,
  torn-tail trim, refusal of newer ring formats, 0700/0600 modes, and an
  in-memory index. `prepare` normalizes identifiers, redacts every body in
  full, maps model-seen bounds through masking and the per-body head/tail cap
  (`stored_trunc` marker), and bounds metadata to 64 KiB (redactions in offset
  order, then one summary per (field, rule); then required meta keys, optional
  keys in key order, and one `meta_truncated` marker).
- The store implements `wsaudit.SourcedSink`. `wsaudit/ident.go`,
  `ident_test.go` and `source.go` are byte-identical to the capture-adapters
  branch (94a00415b), so the second merge is a trivial conflict.
- The daemon owns the store (`wsdashboard/audit.go`, `AuditSink()`), and the
  config keys `dashboard.audit_{session_cap_mib,total_cap_mib,body_cap_kib,retention_days}`
  are registered with defaults 256/4096/1024/7. A set that breaks the knob set
  is refused, and a loaded file that breaks it is clamped with a warning.

Decisions:

- **Identity contract.** Replay identity uses the sibling's `SourcedSink`
  contract verbatim. `Envelope.Source` was dropped, and the ring's event line
  carries the `SourceID`.
- **Recovery answers.** `Recover` and `Retained` answer from a per-ring
  in-memory ledger and never wait on disk. An unloaded ring answers zero and
  queues a load. The daemon calls `Preload` when it first sees a session.
- **Writer rule.** The writer enforces the same rule as the capture side: a
  retained or lost identity is a duplicate, and a covered identity is a loss.
  The boundary moves only through `Finalize` (persisted as fin lines). Loss and
  fin lines are written with the next record or when the writer goes idle.
- **Bounds and sequence.** Seen bounds are stored in stored-body coordinates; a
  cut inside a masked span rounds up past the mask. The store renumbers any seq
  at or below `maxSeq`.

Verification:

- `go test ./internal/wsauditstore/ ./internal/wsaudit/ ./internal/wsredact/ ./internal/wsconfig/`: ok.
- `go test -race -count=1 ./internal/wsauditstore/ ./internal/wsaudit/`: ok
  (250.7s and 1.6s).
- Covering tests: `TestPrepare*` (rule and length kept, straddling secret,
  thousands of matches, call_id normalization, meta overflow, NUL-heavy body,
  smallest-config max record), `TestStoreRestartReplayDedupsAndContinuesSequence`,
  `TestStoreFailedWriteIsLossNeverLateAppend`,
  `TestStoreFinalizedBoundaryRefusesLateRecords`,
  `TestStoreRefusesNewerRingFormat`,
  `TestStoreRecoversIndependentRingsModesAndHeldHandle`,
  `TestStoreSlowOrFailingWriterNeverDelaysProducer`,
  `TestStoreSiblingsOfOneSourceRecordAreAllRetained`, and
  `TestLimitsValidateAndClamp`.
- File ops (`TestStoreFileOpsPerAppendedEvent`, wsstate.FileIOCounters):
  - 50 steady appends: 50 writes and 0 opens.
  - 200 × 60 KiB through a 4 MiB ring with rotation: 230 ops (1.15 per event)
    and 0 reads.
  - The baseline was no audit persistence.
- Compatibility tier: T0 (new on-disk ring under the daemon state directory,
  new optional config keys; no existing format changed).

### Phase 2: Retention and byte caps

Depends on Phase 1. Implement the retention knob (default 7 days after the
session was last seen, Decision 6) and the per-session byte cap (Decision 3)
dropping the oldest whole segment first. Implement least-recently-used
whole-session audit eviction under the per-store total cap (default 4 GiB,
Decision 3). Session deletion removes the
session's ring (Decision 8). There is no purge action (Decision 7).
Record lifecycle timestamps at existing write points for restart-safe
last-seen retention without heartbeat writes (Decision 6).

Verification: the per-session cap drops the oldest whole segments and keeps
the newest. The per-store cap removes whole audit rings in least-recently-used
order. Measure on-disk bytes after both kinds of eviction and check that reads
see only retained events. Expire audit rings for sessions past the configured
idle age and verify that lengthening the period keeps them. Deleting a session
removes its whole audit ring. Verify the config keys resolve and the `ws` and `wsflow`
manifests stay key-identical with only `default` differing.
After registry pruning and daemon restart, verify that the recovered lifecycle
timestamps still age the audit ring by its configured last-seen retention.
Verify a quiet hosted session with heartbeats but no captured events keeps its
ring across restart until the retention knob expires.
When the store total is reached, verify the active ring is evicted last and
keeps its newest segment (Decision 3, (2129)=a).

### Result (12ce5478f) - 2026-11-22

Landed (commits d05aba26b and 12ce5478f on `impl/develop/silk-dairy-cane`):

- **Per-session cap.** The cap drops the oldest whole segment first.
- **Total cap.** The per-store total cap evicts whole rings in
  least-recently-used order. The active ring goes last and keeps its newest
  segment.
- **Retention.** Retention removes rings whose session was last seen more than
  `dashboard.audit_retention_days` ago. The value saturates at
  `MaxRetentionDays` (106751).
- **Session deletion.** Deleting a session removes its whole ring
  (worktree-delete purge path). There is no purge action.

Decisions:

- **Last-seen timestamps.** They come from existing daemon index write points
  only: first sighting, `OnRemove`, and the sweep's registry lookup. They are
  persisted in the ring header snapshot, and nothing writes on heartbeats.
  Registry pruning runs in ws-mcp processes, not in the daemon.
- **Startup sweep.** The first sweep is deferred until the daemon index's first
  `Refresh` (`DeferFirstSweep` plus `RequestSweep`), so it can consult the
  registry.
- **Knob scope.** The knobs are global (not per worktree).
- **User docs.** CHANGELOG plus manifest descriptions in all three plugins,
  and i18n strings.

Verification:

- `go test ./internal/wsauditstore/`: ok.
- `go test -race -count=1 ./internal/wsdashboard/ -run 'TestAudit|TestWorktreeDeletePurgesEndedSessions'`: ok.
- Covering tests: `TestSessionCapDropsOldestWholeSegments`,
  `TestTotalCapEvictsWholeRingsLeastRecentlyUsed`, and
  `TestTotalCapEvictsActiveRingLastKeepingNewest` (on-disk bytes measured,
  reads see only retained events), plus:
  - `TestRetentionExpiresIdleRingsAndLongerKnobKeepsThem`
  - `TestDeleteSessionRemovesWholeRing`
  - `TestLifecycleLastSeenSurvivesRegistryPruneAndRestart`
  - `TestQuietHeartbeatingSessionKeepsRingAcrossRestart`
  - `TestAuditLifecycleRecordsLastSeenAtWritePointsOnly`
  - `TestAuditStartupSweepConsultsTheRegistry`
  - manifest and config tests in `internal/mcp` (`dashboard_audit_config_test.go`)
- Full suite `go test -count=1 -timeout 30m ./...`: the only failures are
  pre-existing.
  - Known develop reds.
  - `internal/mcp`: `TestPlaybookPrintGoldenExplore*`,
    `TestWaitingOnNudgeProductionTicketTreeRestartAndReblock` and
    `TestWhiteboardCommandAcceptanceFirstTextRetryAndSequence`. These also fail
    on b77c9fd69.
  - `internal/wswhiteboard`: `TestDocFrameHandsOverviewKey`, which also fails
    on b77c9fd69.
  - `internal/wsagent`: `TestDaemonStartDeletesAnExpiredSubtreeWithoutALoop`
    and `TestLeadCloseFencesConcurrentPiSpawn`. These are load flakes that pass
    25 of 25 runs in isolation on both HEAD and base, and the runner harness
    does not start the audit store.

Unresolved (minor):

- An unloaded ring evicted by the total cap loses its finalized boundaries.
- LastSeen receives the normalized ID, so reply IDs longer than 256 bytes would
  miss the registry lookup.
