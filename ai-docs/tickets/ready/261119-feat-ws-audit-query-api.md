---
title: "감사 조회·검색 API"
parent: 261119-epic-ws-conversation-audit
blocked-by: 261119-feat-ws-audit-ring-store#phase2
related:
  261119-feat-ws-audit-ring-store: "prerequisite (blocked-by #phase2): the store this API reads, with the eviction and retention state behind cut and stale cursors"
  261119-feat-ws-audit-capture-harness-adapters: "source of the captured events, call IDs, and the call and result timestamps behind duration_ms, and the request usage (meta.request) behind ctx_before and ctx_after"
  261119-feat-ws-dashboard-audit-view: "consumer: every byte the view shows comes from this API"
  261114-feat-ws-conversation-audit-event-store: "superseded source of (599), (681) and the read-API verification"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: b3f3f16065d5b483
sage-review-completeness-reviewed: b3f3f16065d5b483
---

# Audit query and search API

## Background

Fourth child of `261119-epic-ws-conversation-audit` ((1331)=a, 2026-11-19).
It serves the audit store to the dashboard view. The contract below was
approved under the superseded `261114-feat-ws-conversation-audit-event-store`
(owner `594..609,611=ok`, recorded 2026-11-14; (600) was the backend group
acknowledgment) and is carried forward over the per-session audit ring.

## Decisions

1. **Read API contract ((599), carried).** All routes are under the existing
   session prefix, return JSON, and require the same auth as
   `/s/<id>/history`.
   - `GET /s/<id>/audit/events?before=<event id>&after=<event id>&n=<count>&kinds=<k1,k2>`
     pages events, oldest first within the page. `n` defaults to 100, max
     500. It returns `{events: [summary], before, after, end, cut}`, where
     `summary` is `{id, seq, at, kind, subkind, call_id, parent_id, agent_id,
     model_seen, title, in_bytes, out_bytes, attached: [event id]}`.
     `attached` lists the events tied to a call (its result, hooks,
     reminders, subagent events).
     Page selection follows `/s/<id>/history`: with no cursor the page holds
     the newest `n` selected events; `before=<id>` holds the `n` selected
     events immediately older than that event and `after=<id>` the `n`
     immediately newer; both together return HTTP 400. The response
     `before` is the oldest event ID in the page (pass it as `before` for
     older events) and `after` the newest (pass it as `after` for newer
     ones), both `""` on an empty page. `end` is true when no older selected
     event is retained. `cut` is true when `end` is true and the session's
     current ring dropped older segments to capacity eviction, so the view can say older audit data is gone. When
     time retention removed the whole ring (ring-store Decisions 6 and 8)
     no evidence survives, so a ring the session starts afterwards reports
     `cut: false` until it drops a segment itself. Rejected: a persisted
     expiry tombstone, which adds a file write per expiry and outlives the
     data it describes. Rejected: offset paging, which shifts under
     concurrent appends and eviction.
   - `GET /s/<id>/audit/event/<event id>` returns the full envelope (`in`,
     `out`, `meta`, `raw`, `redactions`, `seen`). A body past 8 MiB is served
     as a truncated head with `truncated: true` and a `range` follow-up
     (`?part=out&offset=&len=`). The cap and the follow-up apply to each of
     the three stored bodies, `in`, `out` and `raw` alike: the ring's
     per-body cap can retain more than 8 MiB of `raw` on an `other` event.
     The follow-up `GET /s/<id>/audit/event/<event id>?part=in|out|raw&offset=<n>&len=<n>`
     returns `{part, offset, len, total, data, next, end}`: `data` is the
     stored bytes from `offset` as UTF-8 text, the slice end extended to the
     next character boundary so `len` (the bytes actually returned) may
     exceed the request by up to 3; `total` is the stored length (the
     summary's `in_bytes`/`out_bytes`); `next` is `offset + len`; `end` is
     true when `next == total`. `len` defaults to 1 MiB, max 8 MiB. An
     `offset` past `total`, `len=0`, a `part` the event does not carry, or a
     malformed value returns HTTP 400. An `offset` inside a UTF-8 sequence
     moves forward to the next character boundary, and the response's
     `offset` reports the adjusted value. Rejected: base64 `data`, which the
     text-only view would have to decode for no benefit; and a separate
     smaller response limit for `raw`, which would still leave a retained
     tail unreachable.
   - Two different truncations exist and the API keeps them apart.
     `truncated: true` is response-level: the stored body is larger than one
     response, and `range` pages through it. `stored_trunc` (the envelope
     field, (1737)=b in the store child) is store-level: the store kept only
     head plus tail of an oversized body. The full-envelope route returns
     `stored_trunc` unchanged. `range` offsets address the stored bytes, so
     they page through head, marker and tail, and never reach the omitted
     middle. `stored_trunc` carries the original length. The summary fields
     `in_bytes` and `out_bytes` report stored lengths ((2011), owner
     acknowledged 2026-11-21), describing what `range` can read.
     Rejected: reporting original lengths in those summary fields.
   - `GET /s/<id>/audit/search?q=<text>&regex=0|1&case=0|1&kinds=&n=` searches
     the stored `in` and `out` of the whole conversation, newest first. It
     returns `{hits: [{event_id, kind, field, line_no, line, call_id,
     parent_id}], next}`. `next` is an opaque cursor, `null` after the
     oldest hit; the caller continues with `cursor=<next>` and the same `q`,
     `regex`, `case` and `kinds`. Rejected: exposing an event-ID plus
     line-number pair as request parameters, which would freeze the scan's
     internal position format into the T0 contract.
   - `GET /s/<id>/audit/events?call=<call_id>` returns that call's summary
     plus its attached events, so a click on a ring history row (which holds
     the ring's call ID, (601)) finds its audit events.
   - Additive fields the approved mockup needs: a `tool_call` summary carries
     `duration_ms` (the call's result `at` minus its own `at`, null while the
     call runs), `request_id`, `ctx_before` and `ctx_after`. The last three
     come only from the `meta.request` object the capture
     adapter writes on each captured event that came from a model request
     (capture child, Decision "Request usage"): `{id, prev_id, ctx}`, where
     `ctx` is input + cache_read + cache_creation from that request's usage
     and `prev_id` is the ID of the request the adapter saw immediately
     before it in the same model conversation (the lead's chain or one
     native subagent's), captured or not. `request_id` and `ctx_before` are the call's
     own `meta.request.id` and `ctx`. `ctx_after` is the `ctx` of the
     captured event whose `meta.request.prev_id` equals the call's
     `request_id`, so it is always the immediately following request and
     never a later one. Parallel calls issued in one request share
     `request_id` and get the same values. `request_id` is null when the
     call has no `meta.request` (the harness carries no request ID, or the
     event predates the field); `ctx_before` is null when the call's own
     `ctx` is absent; `ctx_after` is null when no captured event names the
     call's request as `prev_id` (a text-only following reply) or that
     event's `ctx` is absent. A null in one field never clears the others.
     Rejected: deriving `ctx_after` from the next captured event alone, which
     silently skips a text-only request and cannot tell two requests with
     equal totals apart.
   - Rejected: an MCP tool surface as the first consumer surface. It may come
     later as an additive T0 tool. The UI is the consumer.
2. **UTC selection ((681), carried, T0).** `GET /s/<id>/audit/events` also
   accepts optional `since=<RFC3339Nano UTC>` and `until=<RFC3339Nano UTC>`.
   - Select `since <= at < until`. Omitted bounds are unbounded. Malformed
     bounds or `since > until` return HTTP 400. Equal bounds select an empty
     interval.
   - Apply UTC and kinds selection before the event-ID cursors and the `n`
     limit. Preserve oldest-first per-session sequence order, the response
     shape, defaults and maximum page size. `before`/`after` remain event IDs,
     never timestamps.
   - Follow returned ID cursors with unchanged filters until `end`.
     Equal-nanosecond events are distinguished by sequence/event IDs.
   - Timestamp filters select hidden-event ranges only. `?call=` and (601)
     remain the sole history-call join.
   - Rejected: loading the whole retained log and filtering client-side, or a
     separate ring-to-event-ID resolver.
3. **Search scans the audit ring ((1960)=a).** Each search scans one
   session's in-memory index and the stored text from its retained segments
   ((1735)=a) (pending 261119-feat-ws-audit-ring-store). The search contract in Decision 1 is unchanged, including
   regex support and excluding the omitted middle of a `stored_trunc` body.
4. **Compatibility tier T0.** New routes only. Existing routes keep their
   semantics.
5. **The API is read-only, and covers every audited session ((1734)=a,
   (1731)=b, owner 2026-11-19).** No route deletes or purges an event or a
   session's audit data: the owner rejected a purge action (store child
   Decision 7). Data can leave the store through capacity eviction
   ((1970)=a), time retention or session deletion.
   The routes are per session, so a `ws/agent.*` child's audit is read on the
   child's own session. A session with no audit data (a non-hosted session,
   data removed by capacity eviction or time retention, or nothing captured yet)
   returns an empty page, not an error.
6. **Evicted cursors are stale ((2012), owner acknowledged 2026-11-21).**
   An evicted event or search cursor while the session still has audit data
   returns a stale-cursor response; the caller restarts. Rejected: resuming
   at the nearest retained boundary, which would silently leave gaps.
   The stale-cursor response is HTTP 410 Gone with the JSON body
   `{"error": "stale_cursor"}`, the same for event and search cursors.
   Rejected: HTTP 200 with a flag, which a caller can mistake for data, and
   HTTP 400, which conflates an evicted cursor with malformed input.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Implementation conventions (AGENTS.md): `agents-plugin-tool/` ->
  `ai-docs/manuals/shipped-surface-boundary.md`; `agents-plugin-tool/internal/`
  -> `ai-docs/manuals/release-compatibility.md`; `internal/mcp/` ->
  `ai-docs/manuals/ws-mcp.md` (only if an MCP surface is ever added).
- UTC bounds are selection filters only. Call identity and linkage remain
  event/call IDs ((599)/(601)); timestamps never guess joins.
- The `cut` flag and the HTTP 410 stale responses read the live store's
  eviction and retention state from ring-store Phase 2; route tests use a
  fixture store, and Phase 1 also checks them once against a live ring that
  evicted a segment, and once after whole-ring expiry followed by a new
  append (`cut: false`, old cursors stale).
- Reads use the session's audit ring. Phase 1 verifies that capture keeps up
  during a long search.
- Reads never block capture or the store writer; the dashboard's interactive
  paths never wait on file I/O beyond the query itself (parent epic
  file-touch rule).

## Prior Decisions

- 261119-feat-ws-audit-ring-store (2026-11-21, commit): "Audit store moved from SQLite to a per-session segment ring under the owner's storage-type rule (1960)=a and capacity policy (1970)=a." — bearing: constrains
- 261119-epic-ws-conversation-audit (2026-11-19, Cross-Child Decisions): "All audit data reaches the UI through the query API ((599))." — bearing: supports
- 261114-feat-ws-conversation-audit-event-store (2026-11-19, Resolution): "(599) and (681) go to 261119-feat-ws-audit-query-api." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | unknown | route and store implementation files are not named |
| scope.surface | public-interface | GET /s/<id>/audit/events, /event and /search routes |
| scope.new_public_symbol | no | no exported code symbol specified; HTTP routes are new |
| scope.new_type_contract | yes | event summary, envelope, search hits and pagination response contracts |
| scope.test_surface | unknown | route tests on a fixture store are required; test paths are not specified |
| complexity.reuse_points | unconfirmed | audit ring store is pending 261119-feat-ws-audit-ring-store |
| complexity.side_effect_risk | high | queries must not block capture or the store writer |
| risk.correctness | high | filtered cursor pagination, truncation and eviction must preserve boundaries |
| risk.fit | moderate | routes must compose with session history auth and pending ring store |
| risk.test | high | concurrent search, stale cursors and equal-nanosecond pagination require coverage |
| risk.security_or_contract | high | audit bodies require history-equivalent authentication and safe redaction |

## Phases

### Phase 1: Events, event, call lookup and search routes

The route families of Decision 1, with paging, the kinds filter, attached
links, the cut, call lookup, search and the optional UTC bounds of
Decision 2.

Verification: route tests on a fixture store, including paging boundaries,
a truncated large body with its range follow-up (an `in`, an `out` and a
`raw` body, an offset inside a UTF-8 sequence, `len=0`), `request_id`,
`ctx_before` and `ctx_after` over parallel calls, a text-only following
request (null `ctx_after`) and two successive requests with equal totals, a `stored_trunc` body (its
marker returned, summary byte lengths matching stored bytes, range offsets
within the stored bytes, search missing the omitted middle), stale responses
for evicted event and search cursors with retained audit data, a session with
no audit data returning an empty page, no
mutating route registered, and a search hit's line
number and `call_id`. Cover inclusive-lower/exclusive-upper UTC bounds;
omitted, equal, invalid and reversed bounds; kinds and event-ID cursors
combined; and more than 500 equal-nanosecond events across pages without
gaps or duplicates. Confirm requests without UTC bounds keep their contract,
call lookup stays ID-based, and auth matches `/history`. Verify that capture
keeps up during a long search.
