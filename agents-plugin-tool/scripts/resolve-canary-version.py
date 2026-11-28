#!/usr/bin/env python3
"""Resolve develop-direct canary build versions and advisory review-watermark status."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Optional

CORE_RE = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
LEDGER_LINE_RE = re.compile(
    r"^([0-9a-f]{7,40})\.\.([0-9a-f]{7,40}): (pass|concern|block|routed|bootstrap)(?: -> (\S+))?\s*$"
)
CLEARING_VERDICTS = frozenset({"pass", "concern", "bootstrap"})


@dataclass(frozen=True)
class LedgerEntry:
    base: str
    head: str
    verdict: str
    ref: str = ""


def parse_core_version(version: str) -> tuple[int, int, int]:
    match = CORE_RE.fullmatch(version.strip())
    if not match:
        raise ValueError(f"invalid plugin_version {version!r}; expected X.Y.Z")
    return tuple(int(part) for part in match.groups())  # type: ignore[return-value]


def read_runtime_core(repo: Path) -> str:
    versions: dict[str, str] = {}
    for pkg in ("agents-plugin", "agents-plugin-wsflow"):
        runtime_path = repo / pkg / "runtime.json"
        data = json.loads(runtime_path.read_text(encoding="utf-8"))
        plugin_version = str(data.get("plugin_version", "")).strip()
        release_tag = str(data.get("release_tag", "")).strip()
        parse_core_version(plugin_version)
        if release_tag != f"v{plugin_version}":
            raise ValueError(
                f"{pkg}/runtime.json release_tag {release_tag!r} != v{plugin_version}"
            )
        versions[pkg] = plugin_version
    if versions["agents-plugin"] != versions["agents-plugin-wsflow"]:
        raise ValueError(
            "runtime.json plugin_version mismatch: "
            f"{versions['agents-plugin']} != {versions['agents-plugin-wsflow']}"
        )
    return versions["agents-plugin"]


def next_patch_core(core: str) -> str:
    major, minor, patch = parse_core_version(core)
    return f"{major}.{minor}.{patch + 1}"


def parse_claimed_canary_counters(tag_names: Iterable[str], core: str) -> set[int]:
    pattern = re.compile(rf"^v{re.escape(core)}-canary\.(0|[1-9][0-9]*)$")
    claimed: set[int] = set()
    for raw in tag_names:
        name = raw.strip()
        if not name:
            continue
        if name.startswith("refs/tags/"):
            name = name[len("refs/tags/") :]
        if name.endswith("^{}"):
            name = name[:-3]
        match = pattern.fullmatch(name)
        if match:
            claimed.add(int(match.group(1)))
    return claimed


def has_stable_tag(tag_names: Iterable[str], core: str) -> bool:
    expected = f"v{core}"
    for raw in tag_names:
        name = raw.strip()
        if not name:
            continue
        if name.startswith("refs/tags/"):
            name = name[len("refs/tags/") :]
        if name.endswith("^{}"):
            name = name[:-3]
        if name == expected:
            return True
    return False


def next_canary_counter(claimed: set[int]) -> int:
    return max(claimed, default=0) + 1


def fetch_remote_canary_tags(repo: Path, core: str) -> list[str]:
    proc = subprocess.run(
        [
            "git",
            "-C",
            str(repo),
            "ls-remote",
            "--tags",
            "origin",
            f"refs/tags/v{core}",
            f"refs/tags/v{core}-canary.*",
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"git ls-remote failed ({proc.returncode}): {proc.stderr.strip()}")
    tags: list[str] = []
    for line in proc.stdout.splitlines():
        parts = line.split("\t", 1)
        if len(parts) == 2:
            tags.append(parts[1].strip())
    return tags


def parse_release_tag_list(raw: str) -> list[str]:
    """Parse a whitespace/comma-separated release tag list (``--release-tags``).

    Git ref names cannot contain whitespace, so splitting on whitespace and
    commas is lossless for tag names.
    """
    return [token for token in re.split(r"[\s,]+", raw) if token]


def fetch_github_release_tags(repo: Path, github_repository: str) -> list[str]:
    """List GitHub Release tag names via ``gh api`` using the ambient token.

    NOTE: GitHub's REST API hides *draft* releases from tokens that lack push
    access (``contents: write``). A ``contents: read`` token therefore cannot
    see an orphaned draft left by a failed publish, and a counter chosen from
    this list can collide with it. Callers running under a read-only token
    (the ws-canary ``plan`` job) must instead pass a pre-scanned list through
    ``--release-tags`` produced by a write-scoped scan job.
    """
    if not github_repository:
        raise ValueError("--github-repository is required to inspect GitHub Releases")
    proc = subprocess.run(
        [
            "gh",
            "api",
            "--paginate",
            f"repos/{github_repository}/releases?per_page=100",
            "--jq",
            ".[].tag_name",
        ],
        cwd=str(repo),
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"gh api releases failed ({proc.returncode}): {proc.stderr.strip()}")
    return [line.strip() for line in proc.stdout.splitlines() if line.strip()]


def resolve_canary_plan(
    *,
    repo: Path,
    mode: str,
    ref: str,
    signing: str = "eval",
    github_repository: str = "",
    remote_tags: Optional[Iterable[str]] = None,
    release_tags: Optional[Iterable[str]] = None,
    skip_remote: bool = False,
) -> dict[str, str]:
    if mode not in ("artifact", "prerelease"):
        raise ValueError(f"unsupported mode {mode!r}; expected 'artifact' or 'prerelease'")
    if signing not in ("eval", "production"):
        raise ValueError(f"unsupported signing {signing!r}; expected 'eval' or 'production'")

    current_core = read_runtime_core(repo)

    if mode == "artifact":
        if signing == "production" and ref != "refs/heads/develop":
            raise PermissionError(
                f"Production signing in artifact mode is restricted to refs/heads/develop (got {ref!r})."
            )
        eval_signing = signing != "production"
        return {
            "mode": "artifact",
            "current_core": current_core,
            "version": f"{current_core}-canary.0",
            "tag": "",
            "bump_core": "",
            "eval_signing": "true" if eval_signing else "false",
        }

    # prerelease mode
    if ref != "refs/heads/develop":
        raise PermissionError(
            f"Canary prerelease mode requires refs/heads/develop (got {ref!r})."
        )

    target_core = next_patch_core(current_core)
    all_tags: list[str] = []
    if remote_tags is not None:
        all_tags.extend(remote_tags)
    elif not skip_remote:
        all_tags.extend(fetch_remote_canary_tags(repo, target_core))

    if release_tags is not None:
        all_tags.extend(release_tags)
    elif not skip_remote:
        all_tags.extend(fetch_github_release_tags(repo, github_repository))

    if has_stable_tag(all_tags, target_core):
        raise ValueError(
            f"Stable tag v{target_core} already exists on origin; merge main back into develop before cutting a canary."
        )

    claimed = parse_claimed_canary_counters(all_tags, target_core)
    counter = next_canary_counter(claimed)
    version = f"{target_core}-canary.{counter}"
    return {
        "mode": "prerelease",
        "current_core": current_core,
        "version": version,
        "tag": f"v{version}",
        "bump_core": target_core,
        "eval_signing": "false",
    }


def parse_review_ledger(content: str) -> tuple[Optional[LedgerEntry], Optional[LedgerEntry]]:
    frontier: Optional[LedgerEntry] = None
    latest: Optional[LedgerEntry] = None
    for line in content.splitlines():
        match = LEDGER_LINE_RE.match(line)
        if not match:
            continue
        entry = LedgerEntry(
            base=match.group(1),
            head=match.group(2),
            verdict=match.group(3),
            ref=match.group(4) or "",
        )
        latest = entry
        if entry.verdict in CLEARING_VERDICTS:
            frontier = entry
    return frontier, latest


def _git_output(repo: Path, *args: str) -> Optional[str]:
    proc = subprocess.run(
        ["git", "-C", str(repo), *args],
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        return None
    return proc.stdout.strip()


def evaluate_watermark_advisory(repo: Path, target: str = "HEAD") -> list[str]:
    """Return non-blocking GitHub Actions warning lines (empty when clear)."""
    ledger_path = repo / "ai-docs" / ".review-ledger.md"
    if not ledger_path.is_file():
        return [
            "::warning::Review-watermark ledger (ai-docs/.review-ledger.md) is missing; proceeding without review coverage confirmation."
        ]

    frontier, latest = parse_review_ledger(ledger_path.read_text(encoding="utf-8"))
    if frontier is None:
        return [
            "::warning::Review-watermark ledger has no clearing frontier entry; proceeding without review coverage confirmation."
        ]

    target_sha = _git_output(repo, "rev-parse", "--short=9", target) or target
    frontier_short = frontier.head[:9]
    warnings: list[str] = []

    if frontier.verdict == "bootstrap":
        warnings.append(
            f"::warning::Review-watermark frontier is bootstrap ({frontier_short}); {target_sha} has no cleared review sweep yet."
        )
    else:
        is_ancestor = subprocess.run(
            ["git", "-C", str(repo), "merge-base", "--is-ancestor", frontier.head, target],
            capture_output=True,
            text=True,
            check=False,
        )
        if is_ancestor.returncode != 0:
            warnings.append(
                f"::warning::Could not verify {target_sha} against review-watermark frontier ({frontier_short}, {frontier.verdict}); ensure full history is fetched or run a review sweep."
            )
        else:
            ahead_raw = _git_output(repo, "rev-list", "--count", f"{frontier.head}..{target}")
            if ahead_raw is None or not ahead_raw.isdigit():
                warnings.append(
                    f"::warning::Could not count commits ahead of review-watermark frontier ({frontier_short}, {frontier.verdict})."
                )
            else:
                ahead = int(ahead_raw)
                if ahead > 0:
                    noun = "commit" if ahead == 1 else "commits"
                    warnings.append(
                        f"::warning::develop ({target_sha}) is {ahead} {noun} ahead of the review-watermark frontier ({frontier_short}, {frontier.verdict}). Consider running a review sweep."
                    )

    if latest is not None and latest != frontier and latest.verdict == "block":
        ref_note = f" -> {latest.ref}" if latest.ref else ""
        warnings.append(
            f"::warning::Review-watermark ledger has a trailing block entry ({latest.head[:9]}{ref_note}) after the frontier."
        )

    return warnings


def write_github_outputs(path: Path, values: dict[str, str]) -> None:
    with path.open("a", encoding="utf-8", newline="\n") as handle:
        for key, value in values.items():
            if "\n" in value or "\r" in value:
                raise ValueError(f"GitHub output value for {key!r} contains newline")
            handle.write(f"{key}={value}\n")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    res = subparsers.add_parser("resolve", help="Resolve canary build plan")
    res.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
    res.add_argument("--mode", required=True, choices=("artifact", "prerelease"))
    res.add_argument("--ref", required=True)
    res.add_argument("--signing", default="eval", choices=("eval", "production"))
    res.add_argument("--github-repository", default="")
    res.add_argument("--github-output", type=Path)
    res.add_argument(
        "--release-tags",
        default=None,
        help=(
            "Whitespace/comma-separated GitHub Release tag names (including drafts) "
            "pre-scanned with a push-capable token; when given (even empty), gh api is not called"
        ),
    )
    res.add_argument("--skip-remote", action="store_true", help="Skip git ls-remote / gh api (local dry-run only)")
    res.add_argument("--format", choices=("text", "json"), default="text")

    wm = subparsers.add_parser("watermark", help="Emit non-blocking review-watermark advisory")
    wm.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
    wm.add_argument("--target", default="HEAD")

    args = parser.parse_args()
    repo = args.repo.resolve()

    if args.command == "watermark":
        try:
            warnings = evaluate_watermark_advisory(repo, args.target)
        except Exception as exc:  # advisory must never block
            print(f"::warning::Review-watermark advisory check failed: {exc}")
            return 0
        if warnings:
            for line in warnings:
                print(line)
        else:
            frontier, _ = parse_review_ledger((repo / "ai-docs" / ".review-ledger.md").read_text(encoding="utf-8"))
            short_head = frontier.head[:9] if frontier else "unknown"
            verdict = frontier.verdict if frontier else "unknown"
            print(f"Review-watermark frontier ({short_head}, {verdict}) covers {args.target}.")
        return 0

    try:
        plan = resolve_canary_plan(
            repo=repo,
            mode=args.mode,
            ref=args.ref,
            signing=args.signing,
            github_repository=args.github_repository,
            release_tags=(
                parse_release_tag_list(args.release_tags)
                if args.release_tags is not None
                else None
            ),
            skip_remote=args.skip_remote,
        )
    except (ValueError, PermissionError, RuntimeError) as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1

    if args.github_output is not None:
        write_github_outputs(args.github_output, plan)

    if args.format == "json":
        print(json.dumps(plan, indent=2))
    else:
        for key, value in plan.items():
            print(f"{key}={value}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
