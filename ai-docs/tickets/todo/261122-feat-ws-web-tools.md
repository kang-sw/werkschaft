---
title: "ws/web.* 도구 체인: 자체 하네스 전용 웹 검색과 페이지 가져오기"
parent: 261114-epic-ws-common-session-foundation
blocked-by: 261122-feat-ws-builtin-agent-harness#phase1
related:
  261122-feat-ws-provider-auth-layer: "API-key provider for search"
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
   is gated by harness registration data, not a harness-name branch.
2. **Search is the provider's server tool; fetch is a ws MCP tool ((5017),
   technical).** `ws/web.search` is realized by attaching the provider's
   server-side search tool to the builtin harness's model requests
   (`openrouter:web_search` on OpenRouter; the SIWC `web_search` tool where
   the account allows it) over a daemon search tool - OpenRouter offers no
   standalone search endpoint, and the server tool needs no local search code
   or second paid search service. `ws/web.fetch` is a daemon MCP tool that
   fetches one URL and returns readable text, because no provider server tool
   covers fetching a given page.
3. **Default on, capped ((5018), technical).** Search is enabled by default
   with `max_uses` 2 per turn and `max_results` 5, configurable and
   switchable off per config; worst case per turn stays near two searches.
   Rejected: off by default - the owner asked for the capability.
4. **Compatibility tier T0.** A new MCP tool and new config keys.

## Constraints

- Manuals: `ai-docs/manuals/ws-mcp.md`, `ai-docs/manuals/performance.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md`.
- `ws/web.fetch`: http/https only; refuses loopback, link-local, and private
  network addresses (after DNS resolution and on every redirect); response
  size and time caps; HTML reduced to readable text; result truncated to the
  common tool-result budget. No cookies or credentials sent.
- Search usage cost appears in the session's usage/cost events (OpenRouter
  reports it in `usage.cost`).

## Phases

### Phase 1: Server-side search and ws/web.fetch

- Attach the search server tool per Decision 2 and 3 in the harness request
  builder; config keys for enable, `max_uses`, `max_results`, engine.
- `ws/web.fetch` daemon MCP tool with the Constraints above, exposed only to
  builtin harness sessions.
- Verification: request-builder fixture showing the server tool present and
  absent per config; fetch tests for private-address refusal (including
  redirect to a private address), size/time caps, HTML-to-text; a test that
  Claude/Codex/agy sessions do not list `ws/web.fetch`; one live OpenRouter
  search turn recorded in the Result with its reported cost.
