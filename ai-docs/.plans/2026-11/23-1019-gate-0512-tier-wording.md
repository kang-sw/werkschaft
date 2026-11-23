# Plan: v0.51.2 compatibility tier wording for the read-only exec gate

## Request (owner (1399)=a, 2026-11-23 10:18)

The read-only worker exec gate (read-only workers' `exec.spawn` and legacy
`exec.shell` are refused; allowlisted read-only commands only) ships in the
v0.51.2 patch as **T0**. Two reviewers disagreed: the fit reviewer read
`ai-docs/manuals/release-compatibility.md` (~:84-86, "removal without a
working alias is T3") literally as T3; the correctness reviewer judged T0
because the call shape, schema and stored data are unchanged and the refusal
happens before any write. The owner chose T0 with a rationale line in the
manual.

## Outcome

- `CHANGELOG.md`, Unreleased/Changed, the existing read-only exec gate entry
  (added in merge 45f47034f): add the T0 compatibility note in the style the
  CHANGELOG already uses for tier notes (find a precedent; if none, one short
  clause such as "Compatibility: T0 - ..."). State that it is a
  role-authority refusal, not a call-shape change.
- `ai-docs/manuals/release-compatibility.md`, next to the removal/alias rule
  (~:84-86): add ONE line clarifying that refusing a call because of the
  caller's role authority (read-only), with the tool, arguments, schema and
  stored data unchanged and the refusal raised before any write, is a
  permission policy and not a removal; it is T0. Keep it to one line; the
  manual's wording is already tight.

## Verification

- Docs only. Read the diff back; no builds or tests needed.

## Constraints

- Root checkout `D:\Workspace\Repos\devenv` is on `develop`; commit there with
  `ws/git.commit` (expected_branch develop), including this plan file. Do not
  stage `ai-docs/assets/mockups/261123/wb-ask-split/` (another worker owns it).
- No push, no merge. Never print credentials.
- End the commit with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`
  (or your own model's name).

## Return boundary

Stop and report before editing anything other than these two files and this
plan.
