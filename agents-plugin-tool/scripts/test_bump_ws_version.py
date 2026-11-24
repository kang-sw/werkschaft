import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parents[2]
BUMP = "agents-plugin-tool/scripts/bump-ws-version.sh"


class BumpVersionTest(unittest.TestCase):
    def test_updates_current_examples_without_rewriting_history(self):
        paths = [
            BUMP,
            "AGENTS.md",
            "ai-docs/manuals/ws-mcp.md",
            ".github/workflows/ws-mcp-release.yml",
            "agents-plugin-tool/cmd/ws-mcp/main.go",
            "agents-plugin-tool/scripts/build-release-assets.sh",
        ]
        for package in ("agents-plugin", "agents-plugin-wsflow"):
            paths.extend(f"{package}/{path}" for path in (
                ".codex-plugin/plugin.json", ".claude-plugin/plugin.json", "runtime.json",
            ))
        with tempfile.TemporaryDirectory() as directory:
            fixture = Path(directory)
            for path in paths:
                target = fixture / path
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(REPO / path, target)
            manual = fixture / "ai-docs/manuals/ws-mcp.md"
            with manual.open("a", encoding="utf-8") as out:
                out.write("\nHistorical range: v0.51.2, 0.51.2-dev and 0.51.x.\n")
            subprocess.run(["sh", fixture / BUMP, "9.8.7"], check=True, capture_output=True)
            text = manual.read_text(encoding="utf-8")
            self.assertIn("because v0.51.2's file preview", text)
            self.assertIn("Historical range: v0.51.2, 0.51.2-dev and 0.51.x.", text)
            self.assertIn("for example `v9.8.7`", text)
            self.assertIn("Development binaries such as `9.8.7-dev`", text)
            self.assertIn("scripts/build-release-assets.sh 9.8.7-dev", text)


if __name__ == "__main__":
    unittest.main()
