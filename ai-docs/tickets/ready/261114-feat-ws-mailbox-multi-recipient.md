---
title: "공개 To/CC와 비공개 BCC를 지원하는 명시적 mailbox 다중 수신자 발송"
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: e879ecc845e23815
sage-review-design-reviewed: e879ecc845e23815
---

# Explicit mailbox multi-recipient sends with public To/CC and hidden BCC

## Background

Owner decision (787)=a approves explicit multi-recipient mailbox sends, not scope-wide broadcast. A lead can address several known peers in one call while each receiver can see the public audience. The settled contract comes from MAIN's 2026-11-14 relay of the owner's answers; the source investigation `shared/docs/mailbox-multi-recipient.md` is supporting evidence, not authority for its additional proposals.

Existing routing, ordinary envelope queues and per-session history remain the substrate. Public recipients must survive existing body-based delivery and rendering without adding stored envelope fields.

## Decisions

- **Accepted argument shape — (787)=a:** `mailbox.send` accepts `to: string | string[]`, with optional `cc: string[]` and `bcc: string[]`. Preserve existing scalar addressing. To, CC and BCC are delivery destinations, not passive labels.
- **Independent fan-out:** the server delivers one ordinary envelope per recipient. This is explicit finite addressing; there is no all-peers broadcast, stored group, magic mailbox slug or membership expansion.
- **Public context in content:** the server prepends a generated public To/CC header to the content body. All copies carry public To/CC context; BCC is never shown in that public header or disclosed to co-recipients. Preserve the supplied message body after the generated context. Do not depend on a new structured envelope/header field to make the audience visible.
- **Compatibility: T0, additive arguments only.** Retain the current mailbox store and envelope shapes, including ordinary history records. No new store fields, shared-message object, schema-version change, protocol change or grouped-history replacement belongs to this feature.
- **Owner correction: no schema-check or old-daemon guidance.** The daemon serves the tool list; a session that sees `cc` already sees the new daemon's tool surface. Reject the source report's proposed capability-probing instruction, old-daemon argument fallback and additional schema-discovery gate.
- **Header placement after the peer-lead note (lead, 2026-11-22).** A top-level lead's send stamps a peer-lead note as the body's first line (`internal/mcp/mailbox_tools.go#L91-L93`), and the dashboard recognizes that note only on the first line (`internal/wsdashboard/conversation_presentation.go#L69`). Insert the generated public To/CC header after that note line and before the supplied body, so the note stays first. A send without the note puts the header first. Copies of a scalar-`to` send with no CC or BCC carry no header, so existing single-recipient bodies stay byte-identical.
- **Raw body on fan-out (lead, 2026-11-22).** The scalar path trims content through `stringArg` (`internal/mcp/mailbox_tools.go#L37-L47`, `session_state.go#L899-L915`). For a fan-out send (array To, CC or BCC), validate that content is non-empty after trimming, then deliver the supplied bytes unchanged after the generated header. Legacy scalar sends keep the existing trimmed path.
- **Existing leading note (lead, 2026-11-22).** `stampPeerLeadNote` returns content unchanged when it already starts with a peer-lead note, including one from the other product (`mailbox_tools.go#L668-L692`). Treat such a recognized leading note as the note line. Every copy then carries exactly one note, then the public header, then the rest of the message.
- **Mixed outcomes (lead, 2026-11-22).** Enqueue each destination independently, so one recipient's routing or enqueue failure neither rolls back nor stops the other copies, their history entries or their wakes. Today one shared `Store.Update` error discards every staged copy (`wsmailbox/memstore.go#L300-L314`), and the error path drops the success payload (`server_util.go#L98-L128`). A fan-out call returns a sender-facing text receipt that lists the delivered recipients and each failed recipient with its error. When every destination fails, it returns a tool error that carries the same list. This is response text only, not a persisted batch protocol. Scalar responses stay unchanged.
- **Rollback sentence location (lead, 2026-11-22).** `ai-docs/manuals/ws-mcp.md` has no separate rollback runbook. Put the settled sentence in its `### Daemon replacement and process ownership` section, the section that governs daemon swaps. Do not create a rollback procedure; `261115-feat-ws-dev-daemon-accepted-build-recovery` owns that.
- **Only residual rollback instruction:** add one line to the rollback runbook: "After rolling back the daemon, terminal sessions refresh their tool list." This is not a new feature-negotiation procedure or a release-time approval gate.

## Constraints

- Owner instruction (2026-11-22) promotes this ticket to ready and authorizes Phase 1 implementation; the original 2026-11-14 capture carried no implementation authority. Live peer mail and daemon replacement still need their own authorization.
- Read `ai-docs/manuals/shipped-surface-boundary.md` and `ai-docs/manuals/release-compatibility.md` before changing the tooling; read `ai-docs/manuals/ws-mcp.md` for changes under `agents-plugin-tool/internal/mcp/` and the rollback instruction. If shipped workflow guidance changes, also read `ai-docs/manuals/skill-authoring.md` and the matching `ai-docs/manuals/wsflow-mirroring.md` obligations.
- Preserve the existing named-address and reply-ID routing, sender/reply identity, queue ownership, receive/drain behavior, history access and normal reply-to-sender semantics. A public audience is display context, not authority over other sessions or a task-claiming mechanism.
- BCC means hidden from other mail recipients, not secrecy from the sender, local operator or ordinary tool-argument audit. Do not expose the BCC list in delivered body content, recipient history or public dashboard expansion. Do not claim BCC prevents a recipient from revealing their own presence by replying.
- Keep ordinary envelope independence: a recipient's receive must not consume another recipient's copy. Enqueue is not delivery acknowledgement; do not introduce an exactly-once or cross-store atomicity promise.
- Keep errors and successful routing observable without turning fan-out into a new persistent message protocol. This capture does not select a new batch-result schema, numerical fan-out limit, automatic retry, reply-all operation or alias-to-session identity scheme from the investigation's proposals.
- Existing host adapters must display public To/CC through the body they already receive. A host-specific structured-header rollout is not a prerequisite to this T0 feature.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 1 changes send handling, advertised arguments, tests and rollback guidance; current paths include agents-plugin-tool/internal/mcp/mailbox_tools.go, agents-plugin-tool/internal/mcp/server_schemas.go and ai-docs/manuals/ws-mcp.md. |
| scope.surface | public-interface | mailbox.send is an advertised MCP tool; agents-plugin-tool/internal/mcp/server_schemas.go#L831-L842 declares its current arguments. |
| scope.new_public_symbol | no | The existing mailbox.send tool gains arguments; Phase 1 names no new tool or exported symbol. |
| scope.new_type_contract | yes | The public to argument changes from string to string or array and optional cc and bcc arrays are added. |
| scope.test_surface | existing | Existing coverage includes agents-plugin-tool/internal/mcp/mailbox_tools_test.go and agents-plugin-tool/internal/mcp/mailbox_history_test.go; Phase 1 requires additional cases. |
| complexity.reuse_points | confirmed | agents-plugin-tool/internal/mcp/mailbox_tools.go#L37-L118 resolves addresses, stamps identities, enqueues and records history; agents-plugin-tool/internal/wsmailbox/store.go#L86-L97 defines the existing envelope. |
| complexity.side_effect_risk | high | Fan-out mutates independent queues and histories and may partially succeed; BCC must stay out of recipient-visible content. |
| risk.correctness | high | Multiple destinations, route-specific stamps and independent drains must preserve existing semantics; peer-lead first-line stamping also affects header placement. |
| risk.fit | moderate | The body header must coexist with existing host and dashboard presentation, including peer-lead first-line recognition. |
| risk.test | high | Coverage must exercise scalar and array addressing, mixed routes, hidden recipients, independent drains and unchanged serialization. |
| risk.security_or_contract | high | The public MCP argument contract changes and recipient-visible history and dashboard content must not reveal BCC. |

## Phases

### Phase 1: Add explicit fan-out without changing mailbox storage

Implement the settled argument shape and server-generated public audience context on the existing send path. Deliver separate ordinary envelopes to the explicit To/CC/BCC recipients, preserving route-appropriate sender/reply identity and current receive/history semantics. Update the advertised tool arguments and relevant send documentation/tests, without adding the rejected schema-check or old-daemon guidance.

Add only the settled terminal-tool-list refresh sentence to the `### Daemon replacement and process ownership` section of `ai-docs/manuals/ws-mcp.md`. Do not expand this into broadcast, group membership, a store migration, reply-all, a new dashboard grouping feature or a new harness protocol.

Verification:

- Existing scalar To addressing still routes correctly. Array To, CC and BCC reach their explicit destinations through independent ordinary copies, including supported named-address/reply-ID routes.
- A top-level lead's multi-recipient send keeps the peer-lead note on the first line, followed by the public To/CC header and then the body, and the dashboard still recognizes the note. A scalar send with no CC or BCC produces the same body as before.
- Every delivered copy of a multi-recipient send (array To, CC or BCC) shows the generated public To/CC header and original message body, while no co-recipient can recover the BCC list from its received envelope/body/history or dashboard rendering.
- A fan-out send preserves the supplied body bytes, including leading indentation and trailing whitespace, after the header. A scalar send keeps today's trimmed body.
- Forwarding content that already starts with a peer-lead note, from either product, yields one note, then the public header, then the message, and the dashboard still recognizes the note.
- With one failing destination among several, the successful copies are enqueued, recorded in history and woken. The sender's receipt lists delivered and failed recipients, and an all-failed call returns a tool error with that list.
- Receiving one copy leaves other recipients' copies intact. Existing sender/reply identity, queue/drain and per-session history behavior remain functional.
- Store/envelope serialization remains unchanged and no schema-version bump is introduced. Existing body-based host delivery renders the public audience without a new adapter metadata contract.
- The advertised arguments match `to: string | string[]`, optional `cc: string[]`, optional `bcc: string[]`; no all-peers selector is added.
- The `ws-mcp.md` daemon replacement section contains the one settled refresh sentence and no new schema-check/old-daemon guidance is introduced.
- Record exact focused test commands and results during implementation. Live peer mail, daemon replacement and cross-session smoke require their own authorization; this docs capture performs none.
