import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

SCRIPT = Path(__file__).resolve().parent / "resolve-canary-version.py"
SPEC = importlib.util.spec_from_file_location("resolve_canary_version", SCRIPT)
MOD = importlib.util.module_from_spec(SPEC)
sys.modules["resolve_canary_version"] = MOD
assert SPEC.loader is not None
SPEC.loader.exec_module(MOD)


def write_runtime_pair(root: Path, version: str, release_tag: str | None = None) -> None:
    tag = release_tag or f"v{version}"
    for pkg in ("agents-plugin", "agents-plugin-wsflow"):
        pkg_dir = root / pkg
        pkg_dir.mkdir(parents=True, exist_ok=True)
        (pkg_dir / "runtime.json").write_text(
            json.dumps({"plugin_version": version, "release_tag": tag}),
            encoding="utf-8",
        )


class ResolveCanaryVersionTest(unittest.TestCase):
    def test_artifact_mode_uses_current_core_canary_zero_and_eval_by_default(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            plan = MOD.resolve_canary_plan(
                repo=repo,
                mode="artifact",
                ref="refs/heads/feature-branch",
                signing="eval",
                skip_remote=True,
            )
            self.assertEqual(
                plan,
                {
                    "mode": "artifact",
                    "current_core": "0.53.2",
                    "version": "0.53.2-canary.0",
                    "tag": "",
                    "bump_core": "",
                    "eval_signing": "true",
                },
            )

    def test_artifact_mode_refuses_production_signing_off_develop(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            with self.assertRaises(PermissionError):
                MOD.resolve_canary_plan(
                    repo=repo,
                    mode="artifact",
                    ref="refs/heads/feature-branch",
                    signing="production",
                    skip_remote=True,
                )
            plan = MOD.resolve_canary_plan(
                repo=repo,
                mode="artifact",
                ref="refs/heads/develop",
                signing="production",
                skip_remote=True,
            )
            self.assertEqual(plan["eval_signing"], "false")
            self.assertEqual(plan["version"], "0.53.2-canary.0")

    def test_prerelease_mode_requires_develop_and_picks_monotonic_patch_canary(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            with self.assertRaises(PermissionError):
                MOD.resolve_canary_plan(
                    repo=repo,
                    mode="prerelease",
                    ref="refs/heads/main",
                    remote_tags=[],
                    release_tags=[],
                )

            plan = MOD.resolve_canary_plan(
                repo=repo,
                mode="prerelease",
                ref="refs/heads/develop",
                remote_tags=[
                    "refs/tags/v0.53.3-canary.0",
                    "refs/tags/v0.53.3-canary.1",
                    "refs/tags/v0.53.3-canary.1^{}",
                    "refs/tags/v0.53.3-canary.3",
                    "refs/tags/v0.53.3-canary.02",
                    "refs/tags/v0.53.4-canary.2",
                ],
                release_tags=[
                    "v0.53.3-canary.2",  # e.g. draft release without remote tag yet
                    "v0.53.2",
                ],
            )
            self.assertEqual(
                plan,
                {
                    "mode": "prerelease",
                    "current_core": "0.53.2",
                    "version": "0.53.3-canary.4",
                    "tag": "v0.53.3-canary.4",
                    "bump_core": "0.53.3",
                    "eval_signing": "false",
                },
            )

    def test_prerelease_mode_is_strictly_monotonic_across_deleted_gaps(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            plan = MOD.resolve_canary_plan(
                repo=repo,
                mode="prerelease",
                ref="refs/heads/develop",
                remote_tags=[
                    "refs/tags/v0.53.3-canary.1",
                    "refs/tags/v0.53.3-canary.4",
                ],
                release_tags=[],
            )
            self.assertEqual(plan["version"], "0.53.3-canary.5")
            self.assertEqual(plan["tag"], "v0.53.3-canary.5")

    def test_prerelease_mode_refuses_when_next_patch_stable_tag_already_exists(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            for existing in (
                {"remote_tags": ["refs/tags/v0.53.3"], "release_tags": []},
                {"remote_tags": ["refs/tags/v0.53.3^{}"], "release_tags": []},
                {"remote_tags": [], "release_tags": ["v0.53.3"]},
            ):
                with self.assertRaisesRegex(ValueError, "v0.53.3 already exists"):
                    MOD.resolve_canary_plan(
                        repo=repo,
                        mode="prerelease",
                        ref="refs/heads/develop",
                        **existing,
                    )

    def test_prerelease_mode_starts_at_counter_one_when_unclaimed(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            plan = MOD.resolve_canary_plan(
                repo=repo,
                mode="prerelease",
                ref="refs/heads/develop",
                remote_tags=["refs/tags/v0.53.3-canary.0"],
                release_tags=[],
            )
            self.assertEqual(plan["version"], "0.53.3-canary.1")
            self.assertEqual(plan["tag"], "v0.53.3-canary.1")
            self.assertEqual(plan["bump_core"], "0.53.3")

    def test_runtime_contract_mismatch_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            (repo / "agents-plugin-wsflow" / "runtime.json").write_text(
                json.dumps({"plugin_version": "0.53.3", "release_tag": "v0.53.3"}),
                encoding="utf-8",
            )
            with self.assertRaises(ValueError):
                MOD.read_runtime_core(repo)

    def test_watermark_advisory_detects_ahead_and_clean_states(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.email", "test@example.com"], check=True)
            subprocess.run(["git", "-C", str(repo), "config", "user.name", "Test"], check=True)

            (repo / "a.txt").write_text("1\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "a.txt"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-q", "-m", "c1"], check=True)
            c1 = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            (repo / "a.txt").write_text("2\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "a.txt"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-q", "-m", "c2"], check=True)
            c2 = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()

            ledger = repo / "ai-docs" / ".review-ledger.md"
            ledger.parent.mkdir(parents=True, exist_ok=True)
            ledger.write_text(f"# header\n{c1}..{c2}: pass\n", encoding="utf-8")
            self.assertEqual(MOD.evaluate_watermark_advisory(repo, "HEAD"), [])

            # Advance HEAD by 1 commit after frontier, and add a block entry that shouldn't move frontier
            (repo / "a.txt").write_text("3\n", encoding="utf-8")
            subprocess.run(["git", "-C", str(repo), "add", "a.txt"], check=True)
            subprocess.run(["git", "-C", str(repo), "commit", "-q", "-m", "c3"], check=True)
            ledger.write_text(
                f"# header\n{c1}..{c2}: concern\n{c2}..{c2}: block -> 261128-bug-foo\n",
                encoding="utf-8",
            )
            warnings = MOD.evaluate_watermark_advisory(repo, "HEAD")
            self.assertEqual(len(warnings), 2)
            self.assertTrue(warnings[0].startswith("::warning::develop ("))
            self.assertIn("1 commit ahead", warnings[0])
            self.assertIn("trailing block entry", warnings[1])

    def test_cli_offline_smoke_and_github_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            write_runtime_pair(repo, "0.53.2")
            out_file = repo / "gh_output.txt"
            with mock.patch.object(MOD, "fetch_remote_canary_tags", return_value=[]), \
                 mock.patch.object(MOD, "fetch_github_release_tags", return_value=["v0.53.3-canary.1"]):
                plan = MOD.resolve_canary_plan(
                    repo=repo,
                    mode="prerelease",
                    ref="refs/heads/develop",
                    github_repository="kang-sw/werkschaft",
                )
            MOD.write_github_outputs(out_file, plan)
            text = out_file.read_text(encoding="utf-8")
            self.assertIn("version=0.53.3-canary.2\n", text)
            self.assertIn("tag=v0.53.3-canary.2\n", text)
            self.assertIn("bump_core=0.53.3\n", text)


if __name__ == "__main__":
    unittest.main()
