# Plan: Update `ws/git.merge` Size Advisory Resolution Text (1531)=a

- **Scope:** T0 transparent change to `mergeSizeDiagnostic` in `agents-plugin-tool/internal/mcp/git_merge_size.go` and corresponding unit test assertions in `agents-plugin-tool/internal/mcp/git_merge_size_test.go`.
- **Changes:**
  1. Update `Resolution` in `mergeSizeDiagnostic` to preserve `"Do not split this file on your own."` and present 4 explicit choices to the user: (1) refactor/split now if permitted, (2) ignore for now with no `.gitattributes` change, (3) raise limit via `<pattern> ws-size-guard=<margin>`, or (4) silence via `<pattern> -ws-size-guard`.
  2. Verify `server_schemas.go` (`git.merge` description) remains consistent without modification.
  3. Update `TestMergeSizeWarning` in `git_merge_size_test.go` to assert all 4 options are present in `d.Resolution`.
- **Verification:** Run `go test -v ./internal/mcp -run "Test.*MergeSize|Test.*GitMerge"` on remote `w5090` via `D:/sys_lock.sh`.
