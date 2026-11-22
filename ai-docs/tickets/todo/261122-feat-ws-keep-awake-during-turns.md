---
title: "턴이 도는 동안 컴퓨터 절전 막기 (선택 설정, 기본 꺼짐)"
related:
  261120-feat-ws-agy-timeout-process-respawn: "the agy stall diagnosis that motivated this; that ticket keeps respawn and stall observability"
---

# Keep the host awake while hosted turns run (opt-in, default off)

## Background

The 2026-11-22 agy lead stall diagnosis (board topic `agy-lead-stalls`)
verified that the overnight stalls (01:11-07:06 KST) were the Mac's clamshell
sleep and dark-wake cycle: model streams of every running worker broke at the
sleep transitions at the same time (`pmset -g log`). Nothing in ws asks the OS
to stay awake while a hosted lead or a spawned worker has a live turn.

## Decisions

- **Owner (1536)=b, opt-in.** A config setting, default off, makes the
  daemon hold an OS sleep-prevention assertion while at least one hosted or
  spawned agent turn is live, and drop it when none is. Default off keeps
  downstream projects' power behavior unchanged. Rejected: always on (changes
  every user's power use by default); not at all (leaves the observed failure
  to each user).
- **Lead, scope of the assertion.** Prevent idle system sleep only, not
  display sleep. On battery, a closed lid still sleeps on macOS whatever the
  assertion; the setting description says so rather than promising otherwise.
- **Lead, platforms.** macOS: a power assertion equivalent to
  `caffeinate -i` (a child `caffeinate -i -w <daemon pid>` process started
  on the first live turn and stopped on the last is acceptable and needs no
  cgo). Windows: `SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)`
  from a dedicated locked OS thread, cleared on the last turn. Linux and
  others: no-op with a debug log line. Over a per-session assertion: one
  daemon-wide refcount gives one assertion regardless of how many turns run.
- **Lead, compatibility.** T0: one new additive config key in the existing
  manifests; an older daemon ignores the unknown key. The key name and its
  section follow `ai-docs/manuals/config-settings-ui.md`; it is a machine-wide
  daemon setting, so it lives at the scope that manual gives daemon knobs.

## Phases

### Phase 1: Daemon assertion and setting

Add the setting to every config manifest the settings manual lists (ws,
wsflow with the same default, Pi when it carries daemon knobs, dashboard
catalog and translations). Track live turns daemon-wide from the state the
daemon already holds (hosted lead turns and agent.spawn child turns) with a
refcount; take the assertion on 0 -> 1 and release it on 1 -> 0, and release
it on daemon shutdown or when the setting turns off. No file I/O on the turn
path (`ai-docs/manuals/performance.md`).

Verification: unit tests with a fake assertion backend prove acquire on the
first live turn, a single assertion for overlapping turns, release on the last
turn end, release when the setting turns off mid-turn, and no assertion with
the setting off. On macOS, a manual check with the setting on shows the
assertion in `pmset -g assertions` during a turn and gone after it.
`GOOS=windows go vet` passes for the Windows backend.
