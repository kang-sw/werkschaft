---
title: "ws/web.* 도구 체인: 자체 하네스 전용 웹 검색과 페이지 가져오기"
parent: 261114-epic-ws-common-session-foundation
blocked-by: 261122-feat-ws-builtin-agent-harness#phase1
related:
  261122-feat-ws-provider-auth-layer: "API-key provider for search"
  261122-feat-ws-builtin-agent-harness: "Phase 1 prerequisite"
  261123-chore-ws-retire-pi-harness: "Pi's web tools retire without waiting for this ticket's replacement"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 66535ecb0e7e66e8
sage-review-completeness-reviewed: 66535ecb0e7e66e8
---

# ws/web.* tool chain for the builtin harness

## Background

Owner answer (2026-11-22, board (5001) note, verbatim): "web 도구의 경우에도
예, ws/web.* 툴 체인을 따로 만들고, 어차피 claude/codex 같은 하네스의 경우에는
기본적으로 웹 어태치를 지원하니까 논외로 두고 pi 쪽에만 붙이기로 합니다." With
(5002)=a the Pi target became the builtin harness
(`261122-feat-ws-builtin-agent-harness`). The owner also noted: "openrouter에는
API 옵션에 web_search까지 붙일 수 있는 거 같고요".

The retiring Pi extension carried its own `web_search` / `ws_web_fetch`
tools (`agents-plugin-pi/src/web-tools.ts`); this ticket replaces them for the
builtin harness without porting that code.

Verified external facts (read-only research, 2026-11-22):

- OpenRouter's server tool `{"type":"openrouter:web_search","parameters":
  {"engine":...,"max_results":5,"max_uses":2,"max_total_results":10}}` runs
  on OpenRouter's side when the model calls it, on every request format;
  `parameters` is optional; the default engine is provider-native search when
  available, else Exa; `max_tool_calls` caps the shared server-tool budget.
  OpenRouter has no standalone search endpoint outside a model request.
  <https://openrouter.ai/docs/guides/features/server-tools/web-search>.
- Pricing: model tokens plus search - Exa $0.007-$0.015 per search by mode,
  Parallel $0.001-$0.005, Perplexity $0.005, Firecrawl $0.005 plus $0.0125
  per result; native search at provider price.
  <https://openrouter.ai/docs/guides/features/server-tools/web-search#pricing>.
- On the OpenAI SIWC route, web search is conditional on model and
  account/workspace.
  <https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations>.

## Decisions

1. **Builtin harness only (owner (5001) note).** Claude, Codex, and agy keep
   their native web capability; `ws/web.*` is not offered to them. Exposure
   is gated by harness registration data, not a harness-name branch. This
   supersedes `260912-feat-ws-pi-bounded-web-access-for-explore`'s "Pi-local
   only" decision (no web tools in ws-mcp): the owner asked for a `ws/web.*`
   chain, and with Pi retiring the daemon is the only place the builtin
   harness can reach a fetch tool through.
2. **Search is the provider's server tool; fetch is a ws MCP tool ((5017),
   technical).** `ws/web.search` is realized by attaching the provider's
   server-side search tool to the builtin harness's model requests
   (`openrouter:web_search` on OpenRouter; the SIWC `web_search` tool where
   the account allows it) over a daemon search tool - OpenRouter offers no
   standalone search endpoint, and the server tool needs no local search code
   or second paid search service. `ws/web.fetch` is a daemon MCP tool that
   fetches one URL and returns readable text, because no provider server tool
   covers fetching a given page.
3. **Default on, capped per turn by the harness ((5018), technical; cap
   enforcement from sage design review).** Search is enabled by default with
   a budget of 2 searches per turn and `max_results` 5, configurable and
   switchable off per config. The harness enforces the turn budget itself,
   because a turn spans several provider requests and provider limits are
   per request (OpenRouter `max_uses`), ignored by most native engines, or
   absent (SIWC rejects `max_tool_calls`). Before sending a request that
   carries the search tool, the harness reserves the remaining budget and
   sends it as `max_uses`; when the response's usage reports the searches
   actually run, the reservation is reduced to that count; when usage never
   arrives (a cut stream or failed request), the reservation stays charged
   and the retry of that request omits the search tool, since the lost
   attempt may already have spent it. Once the
   budget is spent, the turn's later requests omit the search tool. The
   default OpenRouter engine is `exa`, which honors `max_uses` within a
   request; a native engine is an opt-in config value whose per-request
   overrun the settings text states. SIWC search is opt-in, default off:
   without a request-level cap the budget is best-effort between requests,
   which the settings text states.
   Rejected: off by default - the owner asked for the capability; relying on
   provider caps alone - they do not bound a turn.
4. **Compatibility tier T0.** A new MCP tool and new config keys.

## Constraints

- Manuals: `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/performance.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md`.
- `ws/web.fetch`: http/https only; refuses loopback, link-local, and private
  network addresses (after DNS resolution and on every redirect); response
  size and time caps; HTML reduced to readable text; result truncated to the
  builtin harness's tool-output truncation limit
  (`261122-feat-ws-builtin-agent-harness` Phase 1). No cookies or credentials sent.
- Search usage cost appears in the session's usage/cost events (OpenRouter
  reports it in `usage.cost`).

## Prior Decisions

- 260912-feat-ws-pi-bounded-web-access-for-explore (2026-09-12, Decisions): "**Pi-local only.** Do not add web search/fetch tools or provider configuration to ws-mcp, shared playbooks, or other harness packages." — bearing: contradiction-candidate
- 260912-feat-ws-pi-bounded-web-access-for-explore (2026-09-12, Decisions): "**Own bounded fetch.** ... public HTTP(S) GET retrieval only and returns sanitized text, markdown, or a child-owned cache path under the inline threshold contract" — bearing: supports
- b628ada0 (2026-09-13, commit): "Search transport is direct-only and model-controlled proxy/content/curator parameters are rejected; ws_web_fetch uses native DNS/socket pinning with strict output bounds." — bearing: supports
- e741bc04 (2026-09-12, commit): "Network search/fetch authority is separate from active worker tools: a worker may delegate bounded network reads without exposing a web tool itself." — bearing: constrains
- 261116-refactor-ws-pi-retire-native-agent-tools (2026-11-16, Decisions): "native Explore's `web_search` and `ws_web_fetch` are extension facades ... There is no core web-search/fetc[h]" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | harness request builder, config keys, new daemon MCP tool, tests; reference only: agents-plugin-pi/src/web-tools.ts (tracked at HEAD, absent from sparse worktree) |
| scope.surface | cross-module | new MCP tool ws/web.fetch plus config keys plus harness request builder; per-session exposure through the WS_MCP_ALLOWED_TOOLS allowlist at agents-plugin-tool/internal/mcp/server.go#L181-L182 |
| scope.new_public_symbol | yes | ws/web.fetch MCP tool and the search enable, max_uses, max_results, engine config keys |
| scope.new_type_contract | yes | ws/web.fetch tool input/result schema and the config key set; no such type exists yet (search for web.fetch in agents-plugin-tool returned nothing) |
| scope.test_surface | new-files | ticket requires request-builder fixture, fetch tests and a harness-exposure test; no web.fetch test exists (search returned nothing); agents-plugin-pi/test/web-tools.test.ts is the retiring Pi analogue |
| complexity.reuse_points | unconfirmed | request builder belongs to the unlanded builtin harness (pending 261122-feat-ws-builtin-agent-harness); allowlist and hidden-tool listing exist at agents-plugin-tool/internal/mcp/tracker_support_test.go#L285-L288 |
| complexity.side_effect_risk | high | outbound network fetch from the daemon with SSRF exposure, plus paid search spend per turn |
| risk.correctness | high | private-address refusal must hold after DNS resolution and on every redirect, plus size and time caps |
| risk.fit | moderate | depends on the unlanded builtin harness request builder and on registration-data gating that does not exist yet |
| risk.test | moderate | live OpenRouter search turn is required evidence and server-tool behavior is external |
| risk.security_or_contract | high | new network-egress tool; Prior Decision 260912 said not to add web tools to ws-mcp |

## Phases

### Phase 1: Server-side search and ws/web.fetch

- Attach the search server tool per Decision 2 and 3 in the harness request
  builder (pending 261122-feat-ws-builtin-agent-harness); config keys for enable, `max_uses`, `max_results`, engine.
- `ws/web.fetch` daemon MCP tool with the Constraints above. Admission is
  bound to the caller's trusted hosted registration on the call path
  (`internal/mcp/server_dispatch.go`), not only to listing
  (`internal/mcp/server_inventory.go`): a call from any other harness or an
  unbound session is refused.
- SIWC search attach: if `261122-feat-ws-builtin-agent-harness` Phase 2 has
  not landed, this phase covers OpenRouter only and that ticket's Phase 2
  attaches the SIWC search tool.
- Verification: request-builder fixture showing the server tool present and
  absent per config; fetch tests for private-address refusal (including
  redirect to a private address), size/time caps, HTML-to-text; a test that
  Claude/Codex/agy sessions do not list `ws/web.fetch` and that direct calls
  from them and from an unbound session are refused; a multi-request turn
  fixture showing the search tool omitted once the turn budget is spent; a
  cut-stream fixture showing the reservation stays charged and the retry
  request payload carries no search tool; SIWC search absent by default; one
  live OpenRouter search turn recorded in the Result with its reported cost.
