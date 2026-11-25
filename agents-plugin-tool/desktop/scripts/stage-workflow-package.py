#!/usr/bin/env python3
"""Stage the workflow files required by the offline desktop builder."""
import hashlib
import json
import sys
from pathlib import Path

TREES = (".claude-plugin", ".codex-plugin", "claude-agents", "hooks", "rsrc", "skills", "bin")
FILES = (".mcp.json", "runtime.json", "config-manifest.json", "agy-config-manifest.json", "dashboard-config-manifest.json")


def package_files(root):
    files = {name: (root / name).read_bytes() for name in FILES}
    for tree in TREES:
        for path in sorted((root / tree).rglob("*")):
            if path.is_symlink():
                raise ValueError(f"workflow package contains a link: {path}")
            if path.is_file() and "__pycache__" not in path.parts and not path.name.endswith((".pyc", ".pyo", ".test.ts", ".test.tsx")):
                files[path.relative_to(root).as_posix()] = path.read_bytes()
    # Some workflow distributions carry curated skills without a manifest.
    # The complete-package consumer accepts the existing schema-1 inventory;
    # generate it in the staging directory without modifying those source skill trees.
    if "skills/manifest.json" not in files:
        skills = {name.removeprefix("skills/"): hashlib.sha256(data.replace(b"\r\n", b"\n")).hexdigest()
                  for name, data in sorted(files.items()) if name.startswith("skills/")}
        if not skills:
            raise ValueError(f"incomplete workflow skills: {root}")
        files["skills/manifest.json"] = (json.dumps({"schema_version": 1, "files": skills}, sort_keys=True, indent=2) + "\n").encode()
    for required in (".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/claude-mod/register.ts", "rsrc/manifest.json", "skills/manifest.json", "bin/ws-mcp-launcher.py"):
        if required not in files:
            raise ValueError(f"incomplete workflow package: {root}: {required}")
    return files


def stage_package(source, destination):
    for name, data in package_files(source).items():
        target = destination / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)


if __name__ == "__main__":
    source, destination = map(Path, sys.argv[1:])
    stage_package(source, destination)
