#!/usr/bin/env bash
# scripts/install-claude-plugin.sh — macOS/Linux Werkschaft Claude plugin installer
#
# Usage:
#   ./scripts/install-claude-plugin.sh          Install/refresh Claude plugin snapshot & registration
#   ./scripts/install-claude-plugin.sh update   Same as default (preserved for update callers)
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

info() { printf "\033[1;34m  ➜ %s\033[0m\n" "$*"; }
success() { printf "\033[1;32m  ✔ %s\033[0m\n" "$*"; }
muted() { printf "\033[90m  ✔ %s\033[0m\n" "$*"; }
warn() { printf "\033[1;33m  ⚠ %s\033[0m\n" "$*"; }
die() {
  printf "\033[1;31m  ✘ %s\033[0m\n" "$*" >&2
  exit 1
}
has() { command -v "$1" &>/dev/null; }

usage() {
  cat <<'EOF'
Usage: scripts/install-claude-plugin.sh [update]

Syncs agents-plugin/ to ~/.claude/plugins/ws-plugin/ws (preserving any
.local-devenv-runtime marker), updates ~/.claude/settings.json, ~/.claude.json,
and ~/.claude/plugins/known_marketplaces.json, and installs ws@kang-sw-devenv.
EOF
}

case "${1:-}" in
  "" | update)
    if [[ $# -gt 1 ]]; then
      usage >&2
      exit 2
    fi
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    printf "\033[1;31m  ✘ Unknown argument: %s\033[0m\n" "$1" >&2
    usage >&2
    exit 2
    ;;
esac

has python3 || die "python3 is required on PATH"
[[ -d "$REPO_DIR/agents-plugin" ]] || die "agents-plugin not found under $REPO_DIR"
[[ -f "$REPO_DIR/agents-plugin/.claude-plugin/plugin.json" ]] || die "plugin.json not found under $REPO_DIR/agents-plugin"

# Claude Code legacy cleanup — remove old per-file symlinks created by prior setups.
# Only removes symlinks whose target is inside REPO_DIR — never touches foreign symlinks.
info "Cleaning up old blueprint skill/agent/infra symlinks..."
for dir in "$HOME/.claude/skills" "$HOME/.claude/agents" "$HOME/.claude/infra"; do
  [ -d "$dir" ] || continue
  for entry in "$dir"/*; do
    [ -L "$entry" ] || continue
    if [[ "$(readlink "$entry")" == "$REPO_DIR"* ]]; then
      rm "$entry"
      muted "removed own symlink: $entry"
    fi
  done
done
if [ -L "$HOME/.claude/agents" ] && [[ "$(readlink "$HOME/.claude/agents")" == "$REPO_DIR"* ]]; then
  warn "removing old agents folder symlink"
  rm "$HOME/.claude/agents"
fi
if [ -L "$HOME/.claude/CLAUDE.md" ] && [[ "$(readlink "$HOME/.claude/CLAUDE.md")" == "$REPO_DIR"* ]]; then
  warn "removing retired Claude home instructions symlink"
  rm "$HOME/.claude/CLAUDE.md"
fi
if [ -d "$HOME/.claude/hooks" ]; then
  for entry in "$HOME/.claude/hooks"/*; do
    [ -L "$entry" ] || continue
    if [[ "$(readlink "$entry")" == "$REPO_DIR"* ]]; then
      rm "$entry"
      muted "removed retired hook symlink: $entry"
    fi
  done
fi

# ── ws plugin snapshot copy ───────────────────────────────────────────────────
# Defined early so settings.json and known_marketplaces.json can reference it.
PLUGIN_CACHE="$HOME/.claude/plugins/ws-plugin"

# Stage the complete snapshot before replacing the installed copy.
info "Syncing ws plugin snapshot to $PLUGIN_CACHE/ws/..."
python3 - "$REPO_DIR" "$PLUGIN_CACHE" <<'SNAPSHOTEOF'
import json, shutil, sys, tempfile
from pathlib import Path

repo, cache = map(Path, sys.argv[1:])
source = repo / "agents-plugin"
cache.parent.mkdir(parents=True, exist_ok=True)
stage = Path(tempfile.mkdtemp(prefix=".ws-snapshot-", dir=cache.parent))
backup = stage / "previous"
try:
    shutil.copytree(source, stage / "ws")
    plugin = json.loads((stage / "ws/.claude-plugin/plugin.json").read_text())
    marketplace = {
        "name": "kang-sw-devenv",
        "description": "kang-sw personal devenv plugin marketplace",
        "owner": {"name": "kang-sw"},
        "plugins": [{"name": plugin["name"], "version": plugin.get("version"),
                     "description": plugin.get("description", ""),
                     "author": plugin.get("author", {"name": "kang-sw"}), "source": "./ws"}],
    }
    (stage / ".claude-plugin").mkdir()
    (stage / ".claude-plugin/marketplace.json").write_text(json.dumps(marketplace, indent=2) + "\n")
    # Keep the previous tree until the complete new snapshot is in place.
    ready = stage / "ready"
    ready.mkdir()
    for name in ("ws", ".claude-plugin"):
        (stage / name).rename(ready / name)
    if cache.exists():
        cache.rename(backup)
    try:
        ready.rename(cache)
    except BaseException:
        if backup.exists():
            backup.rename(cache)
        raise
finally:
    if backup.exists() and not cache.exists():
        print(f"Snapshot rollback needs recovery from {backup}", file=sys.stderr)
    else:
        shutil.rmtree(stage)
SNAPSHOTEOF
success "ws plugin snapshot synced"
if [[ -f "$PLUGIN_CACHE/ws/.local-devenv-runtime" ]]; then
  muted ".local-devenv-runtime marker preserved in ws snapshot"
fi

# Claude Code settings — ensure required config is set
info "Ensuring Claude Code settings..."
mkdir -p "$HOME/.claude"
python3 - "$HOME/.claude/settings.json" "$HOME/.claude.json" "$REPO_DIR" "$PLUGIN_CACHE" <<'PYEOF'
import json, sys, os

settings_path, claude_json_path, repo_dir, plugin_cache = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]

# ── settings.json (project-level: env vars) ──────────────────────────────────
required_env = {
    "ENABLE_TOOL_SEARCH": "1",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "300000",
    "CLAUDE_FORK_SUBAGENT": "1"
}

if os.path.isfile(settings_path):
    with open(settings_path) as f:
        settings = json.load(f)
else:
    settings = {}

env = settings.setdefault("env", {})
changed = False
for key, val in required_env.items():
    if env.get(key) != val:
        env[key] = val
        changed = True

# ── retired Claude hook cleanup ──────────────────────────────────────────────
legacy_hook_command = "bash ~/.claude/hooks/teammate-idle-token-tracker.sh"
hooks = settings.get("hooks", {})
for event in list(hooks):
    entries = []
    for entry in hooks.get(event, []):
        kept_hooks = [
            hook for hook in entry.get("hooks", [])
            if hook.get("command") != legacy_hook_command
        ]
        if kept_hooks:
            updated = dict(entry)
            updated["hooks"] = kept_hooks
            entries.append(updated)
        elif entry.get("hooks"):
            changed = True
    if entries:
        if entries != hooks.get(event, []):
            hooks[event] = entries
            changed = True
    else:
        del hooks[event]
        changed = True
if not hooks and "hooks" in settings:
    del settings["hooks"]
    changed = True

# ── blueprint plugin registration ────────────────────────────────────────────
required_marketplaces = {
    "kang-sw-devenv": {
        "source": {"source": "directory", "path": plugin_cache}
    }
}
required_plugins = {"ws@kang-sw-devenv": True}

obsolete_marketplaces = {"ws"}
obsolete_plugins = {"ws@ws", "wsflow@kang-sw-devenv"}

marketplaces = settings.setdefault("extraKnownMarketplaces", {})
for name in obsolete_marketplaces:
    if name in marketplaces:
        del marketplaces[name]
        changed = True
for name, cfg in required_marketplaces.items():
    if marketplaces.get(name) != cfg:
        marketplaces[name] = cfg
        changed = True

plugins = settings.setdefault("enabledPlugins", {})
for name in obsolete_plugins:
    if name in plugins:
        del plugins[name]
        changed = True
for name, val in required_plugins.items():
    if plugins.get(name) != val:
        plugins[name] = val
        changed = True

if changed:
    with open(settings_path, "w") as f:
        json.dump(settings, f, indent=2)
        f.write("\n")
    print("  \033[1;32m  ✔ Claude Code settings.json updated\033[0m")
else:
    print("  \033[90m  ✔ Claude Code settings.json already current\033[0m")

# ── claude.json (global user config) ─────────────────────────────────────────
required_global = {
    "teammateMode": "in-process",
}

if os.path.isfile(claude_json_path):
    with open(claude_json_path) as f:
        global_cfg = json.load(f)
else:
    global_cfg = {}

changed = False
for key, val in required_global.items():
    if global_cfg.get(key) != val:
        global_cfg[key] = val
        changed = True

if changed:
    with open(claude_json_path, "w") as f:
        json.dump(global_cfg, f, indent=2)
        f.write("\n")
    print("  \033[1;32m  ✔ Claude Code claude.json updated\033[0m")
else:
    print("  \033[90m  ✔ Claude Code claude.json already current\033[0m")
PYEOF

# Pre-register marketplace in known_marketplaces.json so `claude plugin install` can resolve it
# before Claude Code has had a chance to process extraKnownMarketplaces itself.
mkdir -p "$HOME/.claude/plugins"
python3 - "$HOME/.claude/plugins/known_marketplaces.json" "$PLUGIN_CACHE" "$REPO_DIR" <<'PYEOF'
import json, sys, os
from datetime import datetime, timezone

km_path, plugin_cache, repo_dir = sys.argv[1], sys.argv[2], sys.argv[3]
km = json.load(open(km_path)) if os.path.isfile(km_path) else {}

entry = {
    "source": {"source": "directory", "path": plugin_cache},
    "installLocation": plugin_cache,
    "lastUpdated": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.") + f"{datetime.now(timezone.utc).microsecond // 1000:03d}Z"
}
if km.get("kang-sw-devenv") == entry:
    print("  \033[90m  ✔ kang-sw-devenv marketplace already registered\033[0m")
else:
    km["kang-sw-devenv"] = entry
    with open(km_path, "w") as f:
        json.dump(km, f, indent=2)
        f.write("\n")
    print("  \033[1;32m  ✔ kang-sw-devenv marketplace registered\033[0m")
PYEOF

install_claude_plugin() {
  local plugin_name="$1"
  local plugin_ref="${plugin_name}@kang-sw-devenv"

  if [[ -f "$INSTALLED_PLUGINS" ]] && python3 -c "
import json, sys
d = json.load(open(sys.argv[1]))
p = d.get('plugins', {}).get(sys.argv[3], {})
sys.exit(0 if p and p.get('installLocation') == sys.argv[2] else 1)
" "$INSTALLED_PLUGINS" "$PLUGIN_CACHE" "$plugin_ref" 2>/dev/null; then
    muted "$plugin_name plugin already installed at snapshot path"
  else
    info "Installing $plugin_name plugin..."
    # Remove stale entry so claude plugin install writes a fresh installLocation.
    # Version equality would otherwise cause the install to no-op without updating the path.
    if [[ -f "$INSTALLED_PLUGINS" ]]; then
      python3 - "$INSTALLED_PLUGINS" "$plugin_ref" <<'PYEOF'
import json, sys
path, plugin_ref = sys.argv[1], sys.argv[2]
d = json.load(open(path))
if plugin_ref in d.get('plugins', {}):
    del d['plugins'][plugin_ref]
    with open(path, 'w') as f:
        json.dump(d, f, indent=2)
        f.write('\n')
PYEOF
    fi
    claude plugin install "$plugin_ref" && success "$plugin_name plugin installed" || warn "$plugin_name plugin install failed — run manually: claude plugin install $plugin_ref"
  fi
}

if has claude; then
  INSTALLED_PLUGINS="$HOME/.claude/plugins/installed_plugins.json"
  if [[ -f "$INSTALLED_PLUGINS" ]]; then
    python3 - "$INSTALLED_PLUGINS" <<'PYEOF'
import json, sys
path = sys.argv[1]
d = json.load(open(path))
plugins = d.get("plugins", {})
if "wsflow@kang-sw-devenv" in plugins:
    del plugins["wsflow@kang-sw-devenv"]
    with open(path, "w") as f:
        json.dump(d, f, indent=2)
        f.write("\n")
    print("  \033[1;32m  ✔ removed wsflow Claude plugin install record\033[0m")
PYEOF
  fi
  install_claude_plugin ws
else
  warn "claude not found — run manually after install: claude plugin install ws@kang-sw-devenv"
fi

# Clean up dead symlinks (skills and agents)
cleanup_dead_links() {
  local dir="$1"
  for entry in "$dir"/*; do
    [ -L "$entry" ] || continue
    if [ ! -e "$entry" ]; then
      warn "removing dead link: $entry"
      rm "$entry"
    fi
  done
}
cleanup_dead_links "$HOME/.claude/skills"
cleanup_dead_links "$HOME/.claude/agents"

echo ""
success "Done! Restart Claude Code to load the updated ws plugin."
