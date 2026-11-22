---
title: "Windows 호스트에서 develop 기준으로 실패하는 Go 테스트(가짜 holder 하네스, CRLF, 상대 경로) 정리"
---

# Go tests that fail on a native Windows host at develop

## Background

Native-Windows verification of three recent branches showed a stable set of
test failures. Each one also fails on develop, so each is a test-environment
defect, not a regression in the branch under test. Every lead now re-proves that
for each branch. Those failures also hide a real Windows regression in the
same tests. This ticket makes these tests pass, or skip with a stated reason, on
a native Windows host.

How they were observed: each package was cross-compiled
(`GOOS=windows GOARCH=amd64 go test -c`), copied to the Windows host and run
from a scratch directory outside the source tree, with a `-test.run` filter.
The scratch directory matters: tests that read repository files by relative
path fail that way. Reproduced on develop at d80f7076b (wsagent) and at
1c485b9be (wsholder).

Failing groups:

1. **wsagent fake-holder lead harness** (`newLeadHarness`). Each test fails with
   "timed out waiting for a process under a holder":
   - TestAdmitCompletionOnceAcrossRestartDrainAndCrash
   - TestAdmitCompletionFencedByClose
   - TestPassiveHandStartsNoTurnAndRidesTheNextSend
   - TestPassiveReceiptSurvivesAFailedSend
   - TestArchivedLeadPassiveInputAndMailStayQueued
   - TestUserStopNoticeSteersARunningTurnWakesAnIdleOneAndDeduplicates
2. **wsholder:**
   - TestAuditChannelTeesSkippedLinesToANegotiatingDaemon. Lines arrive as
     `"drop a\r\n"`; the test expects LF.
   - TestSpawnHTTP2CaptureFiltersStderr. Records are
     `"plain before\r\nplain after\r\n"`; the test expects LF-only lines.
   - TestAuditChannelOldHolderAttachesWithoutIt. Fails with
     `open testdata/holder-pre-audit.py: The system cannot find the file specified`
     (relative testdata path).
3. **mcp `Session`-filter tests**, for example
   TestDashboardSessionFingerprintTracksTheInbox, TestHostedSessionKeyFillsAnOmittedKey
   and TestReloginToAHostedSessionWithNoProcessTakesItsRow. They read
   `..\..\..\agents-plugin\*-manifest.json` and the rsrc dev checkout by relative
   path.
4. **wsdashboard node-asset tests:** TestComposerInlineWireAndHelpers,
   TestComposerAutosizeClient and TestComposerNoteChipsBehavior.

## Decisions

- Distinguish a real Windows defect from a harness assumption before changing
  code. If a failure turns out to be a product bug (for example, holder stderr
  records keeping CR), fix the product, not the test expectation, and say so in
  the Result. (Technical decision: a test that hides a product CRLF leak would
  be worse than the failure.)
- A test that needs repository files may skip only when those files are absent
  (the test binary runs outside the tree), and must say so in the skip message.
  A silent pass is not allowed. This matches the existing chmod-skip concern in
  260728-bug-chmod-based-tests-skip-silently.
- No new CI job. Verification is the cross-compiled run on the Windows host
  described in Background.

## Constraints

- Apply `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/performance.md` and `ai-docs/manuals/release-compatibility.md`
  for `agents-plugin-tool/`, and `ai-docs/manuals/ws-mcp.md` for
  `agents-plugin-tool/internal/mcp/`, per AGENTS.md Implementation Conventions.
- Tests run with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local` and narrow `-run`
  filters. Never run the whole `./internal/mcp` or `./internal/wsdashboard`.
- Workers do not connect to the Windows host. The lead runs the Windows step.
  Workers run `GOOS=windows go vet` on touched packages.

## Phases

### Phase 1: Make the four groups pass or skip with a reason on Windows

For each group, find the cause, then apply the smallest fix:

- **Group 1.** Find why the fake holder's child process is never seen under the
  holder on Windows (process discovery, the shell or the script interpreter).
  Fix the harness, or the product if the product is wrong.
- **Group 2.** Normalize line endings where the test compares text, or strip CR
  in the product if holder records are meant to be LF. Resolve testdata relative
  to the test source (`runtime.Caller`) or skip when it is absent.
- **Group 3.** Locate manifests and rsrc relative to the test source file, or
  skip with a reason when the repository is absent.
- **Group 4.** Find the cause (the node runtime or the asset path on the host),
  then fix it or skip with a reason.

Acceptance:

- macOS: the named tests still pass.
- Windows host, run by the lead: every named test passes, or skips with a
  message that names the missing prerequisite.
- The Result lists each group's cause and fix, and marks product fixes
  separately.
