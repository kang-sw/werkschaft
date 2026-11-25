#!/usr/bin/env bash
# Runs one shard of the Go test suite for ws-mcp CI.
#
#   ci-test-shard.sh all                  every package except the long group
#   ci-test-shard.sh long                 only the explicitly requested long group
#   ci-test-shard.sh mcp [I N]            internal/mcp whole, or top-level tests
#                                         whose sorted index is I mod N
#   ci-test-shard.sh wsdashboard [I N]    internal/wsdashboard whole, or top-level
#                                         tests whose sorted index is I mod N
#   ci-test-shard.sh rest [I N]           every package except internal/mcp,
#                                         internal/wsdashboard, and long; when
#                                         I N are given, selects shard I of N
#                                         (balanced heavy-package pins + sorted
#                                         mod assignment for all other packages)
#   ci-test-shard.sh verify               prove the CI shard partitions cover
#                                         100% of packages and sharded top-level
#                                         tests with zero gaps and zero duplicates
#
# `mcp` (or `mcp 0..3 4`), `wsdashboard 0..1 2`, and `rest 0..2 3` partition
# `all` with zero gaps and zero duplicates. Adding `long` covers the entire
# module. Extra arguments after the shard selector are passed to `go test`
# (for example -list to preview a shard without running tests).
#
# -count=1 bypasses cached test results: CI restores GOCACHE, and a cached pass
# would skip tests whose inputs go test cannot see (git subprocesses, the
# runner's tools). The restored compile cache still applies.
set -euo pipefail

cd "$(dirname "$0")/.."

# Keep this script compatible with macOS /bin/bash 3.2 for ship pre-flight
# (no associative arrays, no mapfile/readarray).
long_pkgs=(./internal/wsledger ./internal/wstrackerhost)

is_nonneg_int() {
  [[ "${1:-}" =~ ^[0-9]+$ ]]
}

is_long_pkg() {
  local pkg="$1"
  local long_pkg
  for long_pkg in "${long_pkgs[@]}"; do
    if [[ "$pkg" == */"${long_pkg#./}" ]]; then
      return 0
    fi
  done
  return 1
}

# Pin heavy packages to distinct rest buckets so both Windows and Ubuntu
# 3-way rest shards stay balanced (~10m max on Windows, ~4.5m max on Ubuntu).
# Returns 0 and prints the preferred bucket index if pinned; returns 1 otherwise.
rest_pinned_bucket() {
  local pkg="$1"
  case "$pkg" in
    */cmd/ws-mcp)
      echo 0
      return 0
      ;;
    */internal/wsagent|*/internal/wsgit|*/internal/wstracker|*/internal/wslive)
      echo 1
      return 0
      ;;
    */internal/wswhiteboard|*/internal/wsindex|*/internal/wsauditstore|*/internal/wsdoc)
      echo 2
      return 0
      ;;
  esac
  return 1
}

# Print packages for `all` or `rest [I N]` (one per line, deterministically sorted).
select_pkg_list() {
  local sel_mode="$1"
  local index="${2:-0}"
  local count="${3:-1}"
  if ! is_nonneg_int "$index" || ! is_nonneg_int "$count" || (( count <= 0 || index >= count )); then
    echo "invalid shard selector: $sel_mode $index $count" >&2
    exit 2
  fi

  local packages
  packages="$(go list ./... | LC_ALL=C sort)"

  local rest_unpinned=()
  local picked=()
  local pkg bucket
  while IFS= read -r pkg; do
    [[ -z "$pkg" ]] && continue
    if is_long_pkg "$pkg"; then
      continue
    fi
    if [[ "$sel_mode" == all ]]; then
      picked+=("$pkg")
      continue
    fi
    if [[ "$pkg" == */internal/mcp || "$pkg" == */internal/wsdashboard ]]; then
      continue
    fi
    if (( count == 1 )); then
      picked+=("$pkg")
      continue
    fi
    if bucket="$(rest_pinned_bucket "$pkg")"; then
      if (( bucket % count == index )); then
        picked+=("$pkg")
      fi
    else
      rest_unpinned+=("$pkg")
    fi
  done <<< "$packages"

  if [[ "$sel_mode" == rest && "$count" -gt 1 && "${#rest_unpinned[@]}" -gt 0 ]]; then
    local i
    for i in "${!rest_unpinned[@]}"; do
      if (( i % count == index )); then
        picked+=("${rest_unpinned[$i]}")
      fi
    done
  fi

  if (( ${#picked[@]} == 0 )); then
    echo "shard $sel_mode $index/$count selected no packages" >&2
    exit 1
  fi
  printf '%s\n' "${picked[@]}" | LC_ALL=C sort
}

# Print top-level test names for `<pkg> I N` (one per line).
select_pkg_test_names() {
  local pkg="$1"
  local index="$2"
  local count="$3"
  if ! is_nonneg_int "$index" || ! is_nonneg_int "$count" || (( count <= 0 || index >= count )); then
    echo "invalid shard selector: $pkg $index $count" >&2
    exit 2
  fi
  local listed
  listed="$(go test -list . "$pkg" | grep -E '^(Test|Example|Fuzz)' | LC_ALL=C sort)"
  local names=()
  local name
  while IFS= read -r name; do
    [[ -z "$name" ]] && continue
    names+=("$name")
  done <<< "$listed"
  local picked=()
  local i
  for i in "${!names[@]}"; do
    if (( i % count == index )); then
      picked+=("${names[$i]}")
    fi
  done
  if (( ${#picked[@]} == 0 )); then
    echo "shard $pkg $index/$count selected no tests" >&2
    exit 1
  fi
  printf '%s\n' "${picked[@]}"
}

run_pkg_shard() {
  local pkg="$1"
  shift
  if (( $# >= 2 )) && is_nonneg_int "$1" && is_nonneg_int "$2"; then
    local index="$1"
    local count="$2"
    shift 2
    local selected
    selected="$(select_pkg_test_names "$pkg" "$index" "$count")"
    local picked=()
    local name
    while IFS= read -r name; do
      [[ -z "$name" ]] && continue
      picked+=("$name")
    done <<< "$selected"
    local pattern
    pattern="^($(IFS='|'; echo "${picked[*]}"))\$"
    exec go test -count=1 -timeout 30m -run "$pattern" "$@" "$pkg"
  fi
  exec go test -count=1 -timeout 30m "$@" "$pkg"
}

verify_shards() {
  local all_pkgs default_pkgs long_resolved
  all_pkgs="$(go list ./... | LC_ALL=C sort)"
  default_pkgs="$(select_pkg_list all)"

  # Resolve long_pkgs from go list ./... for exact module-path comparison.
  long_resolved="$(while IFS= read -r pkg; do
    [[ -z "$pkg" ]] && continue
    if is_long_pkg "$pkg"; then
      printf '%s\n' "$pkg"
    fi
  done <<< "$all_pkgs" | LC_ALL=C sort)"

  # Verify default + long == all module packages with zero gaps/duplicates.
  local combined_all dupes
  combined_all="$(printf '%s\n%s\n' "$default_pkgs" "$long_resolved" | grep -v '^$' | LC_ALL=C sort)"
  dupes="$(printf '%s\n' "$combined_all" | uniq -d)"
  if [[ -n "$dupes" ]]; then
    echo "verify failed: duplicate packages between default and long:" >&2
    printf '%s\n' "$dupes" >&2
    exit 1
  fi
  if [[ "$combined_all" != "$all_pkgs" ]]; then
    echo "verify failed: default + long does not match go list ./..." >&2
    exit 1
  fi

  # Verify mcp + wsdashboard + rest 0..2 3 == default_pkgs with zero gaps/duplicates.
  local mcp_pkg dash_pkg
  mcp_pkg="$(printf '%s\n' "$all_pkgs" | grep -E '/internal/mcp$' || true)"
  dash_pkg="$(printf '%s\n' "$all_pkgs" | grep -E '/internal/wsdashboard$' || true)"
  local rest0 rest1 rest2
  rest0="$(select_pkg_list rest 0 3)"
  rest1="$(select_pkg_list rest 1 3)"
  rest2="$(select_pkg_list rest 2 3)"

  local matrix_pkgs
  matrix_pkgs="$(printf '%s\n%s\n%s\n%s\n%s\n' "$mcp_pkg" "$dash_pkg" "$rest0" "$rest1" "$rest2" | grep -v '^$' | LC_ALL=C sort)"
  dupes="$(printf '%s\n' "$matrix_pkgs" | uniq -d)"
  if [[ -n "$dupes" ]]; then
    echo "verify failed: duplicate packages across CI shards:" >&2
    printf '%s\n' "$dupes" >&2
    exit 1
  fi
  if [[ "$matrix_pkgs" != "$default_pkgs" ]]; then
    echo "verify failed: CI shards do not cover 100% of default packages" >&2
    exit 1
  fi

  # Verify test-level sharding for mcp (4-way) and wsdashboard (2-way).
  local pkg n i all_tests shard_tests
  for spec in "./internal/mcp:4" "./internal/wsdashboard:2"; do
    pkg="${spec%%:*}"
    n="${spec##*:}"
    all_tests="$(go test -list . "$pkg" | grep -E '^(Test|Example|Fuzz)' | LC_ALL=C sort)"
    shard_tests=""
    for (( i = 0; i < n; i++ )); do
      shard_tests+="$(select_pkg_test_names "$pkg" "$i" "$n")"$'\n'
    done
    shard_tests="$(printf '%s' "$shard_tests" | grep -v '^$' | LC_ALL=C sort)"
    dupes="$(printf '%s\n' "$shard_tests" | uniq -d)"
    if [[ -n "$dupes" ]]; then
      echo "verify failed: duplicate tests across $pkg shards:" >&2
      printf '%s\n' "$dupes" >&2
      exit 1
    fi
    if [[ "$shard_tests" != "$all_tests" ]]; then
      echo "verify failed: $pkg shards do not cover 100% of top-level tests" >&2
      exit 1
    fi
  done

  local total_count default_count r0_count r1_count r2_count
  total_count="$(printf '%s\n' "$all_pkgs" | grep -c .)"
  default_count="$(printf '%s\n' "$default_pkgs" | grep -c .)"
  r0_count="$(printf '%s\n' "$rest0" | grep -c .)"
  r1_count="$(printf '%s\n' "$rest1" | grep -c .)"
  r2_count="$(printf '%s\n' "$rest2" | grep -c .)"
  echo "OK: $total_count packages total ($default_count default = mcp + wsdashboard + rest[$r0_count,$r1_count,$r2_count], plus ${#long_pkgs[@]} long); mcp/4 and wsdashboard/2 test shards verified with zero gaps and zero duplicates."
}

mode="${1:?usage: ci-test-shard.sh all|rest [I N]|long|mcp [I N]|wsdashboard [I N]|verify [go test args...]}"
shift

case "$mode" in
  all)
    selected="$(select_pkg_list all)"
    pkgs=()
    while IFS= read -r pkg; do
      [[ -z "$pkg" ]] && continue
      pkgs+=("$pkg")
    done <<< "$selected"
    exec go test -count=1 -timeout 30m "$@" "${pkgs[@]}"
    ;;
  rest)
    index=0
    count=1
    if (( $# >= 2 )) && is_nonneg_int "$1" && is_nonneg_int "$2"; then
      index="$1"
      count="$2"
      shift 2
    fi
    selected="$(select_pkg_list rest "$index" "$count")"
    pkgs=()
    while IFS= read -r pkg; do
      [[ -z "$pkg" ]] && continue
      pkgs+=("$pkg")
    done <<< "$selected"
    exec go test -count=1 -timeout 30m "$@" "${pkgs[@]}"
    ;;
  long)
    exec go test -count=1 -timeout 30m "$@" "${long_pkgs[@]}"
    ;;
  mcp)
    run_pkg_shard ./internal/mcp "$@"
    ;;
  wsdashboard)
    run_pkg_shard ./internal/wsdashboard "$@"
    ;;
  verify)
    verify_shards
    ;;
  *)
    echo "unknown shard mode: $mode" >&2
    exit 2
    ;;
esac
