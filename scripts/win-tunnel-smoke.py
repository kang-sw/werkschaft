#!/usr/bin/env python3
"""Verify the Windows ws-mcp.exe dashboard daemon over an SSH tunnel.

Builds ws-mcp.exe for windows/amd64 (unless --remote-binary is passed), runs it
inside an isolated scratch directory on a remote Windows host over SSH, tunnels
its loopback port via `ssh -L`, verifies health, cookie/link auth, and HTML
routes, and optionally runs a headless Chromium Playwright check on macOS.
"""
from __future__ import annotations

import argparse
import base64
from dataclasses import dataclass, field
import http.cookiejar
import json
import os
from pathlib import Path
import random
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


REPO_ROOT = Path(__file__).resolve().parent.parent
TOOL_DIR = REPO_ROOT / "agents-plugin-tool"
ASSET_GEN_SCRIPT = TOOL_DIR / "scripts" / "generate-package-assets.py"

SSH_BASE_OPTS = (
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=10",
)


class SmokeError(RuntimeError):
    """Raised when a smoke precondition or assertion fails."""


@dataclass
class SmokeContext:
    host: str
    remote_port: int
    local_port: int
    remote_scratch: str | None = None
    remote_pid: int | None = None
    token: str | None = None
    tunnel_proc: subprocess.Popen[str] | None = None
    local_tmp: Path | None = None
    cleaned_up: bool = False
    timings: dict[str, float] = field(default_factory=dict)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Verify Windows ws-mcp.exe dashboard daemon over SSH tunnel.",
    )
    parser.add_argument(
        "--host",
        default=os.environ.get("WS_WIN_HOST", ""),
        help="Windows SSH target ([user@]host). Defaults to WS_WIN_HOST.",
    )
    parser.add_argument(
        "--remote-binary",
        default=None,
        help="Existing Windows path to ws-mcp.exe (skips local cross-compile and scp).",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=0,
        help="Local (and preferred remote) loopback port; 0 picks free ports automatically.",
    )
    parser.add_argument(
        "--no-browser",
        action="store_true",
        help="Skip Playwright Chromium browser verification.",
    )
    parser.add_argument(
        "--keep-open",
        action="store_true",
        help="Keep daemon and SSH tunnel open after checks pass until Ctrl+C.",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=30.0,
        help="Timeout in seconds for startup/HTTP/browser checks (default: 30).",
    )
    args = parser.parse_args(argv)
    args.host = args.host.strip()
    if not args.host:
        parser.error("missing target host: pass --host or set WS_WIN_HOST")
    if args.port < 0 or args.port > 65535:
        parser.error("--port must be between 0 and 65535")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    return args


def pick_free_local_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def pick_remote_port(requested: int) -> int:
    if requested > 0:
        return requested
    return random.randint(38000, 48000)


def ps_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def encode_powershell(script: str) -> str:
    return base64.b64encode(script.encode("utf-16le")).decode("ascii")


def run_ssh_powershell(
    host: str,
    script: str,
    *,
    timeout: float = 20.0,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    encoded = encode_powershell(script)
    cmd = [
        "ssh",
        *SSH_BASE_OPTS,
        host,
        "powershell",
        "-NoProfile",
        "-NonInteractive",
        "-EncodedCommand",
        encoded,
    ]
    proc = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=timeout,
        check=False,
    )
    if check and proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        raise SmokeError(f"SSH command failed (exit {proc.returncode}): {detail}")
    return proc


def build_windows_binary(local_tmp: Path) -> Path:
    go = shutil.which("go")
    if not go:
        raise SmokeError("go is not on PATH; cannot cross-compile ws-mcp.exe")
    if ASSET_GEN_SCRIPT.is_file():
        gen_proc = subprocess.run(
            [sys.executable, str(ASSET_GEN_SCRIPT)],
            cwd=str(TOOL_DIR),
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            check=False,
        )
        if gen_proc.returncode != 0:
            detail = (gen_proc.stderr or gen_proc.stdout or "").strip()
            raise SmokeError(f"generate-package-assets.py failed ({gen_proc.returncode}): {detail}")
    out_exe = local_tmp / "ws-mcp.exe"
    env = os.environ.copy()
    env["GOOS"] = "windows"
    env["GOARCH"] = "amd64"
    env["CGO_ENABLED"] = "0"
    proc = subprocess.run(
        [go, "build", "-o", str(out_exe), "./cmd/ws-mcp"],
        cwd=str(TOOL_DIR),
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )
    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        raise SmokeError(f"GOOS=windows GOARCH=amd64 go build failed ({proc.returncode}): {detail}")
    if not out_exe.is_file():
        raise SmokeError(f"go build succeeded without producing {out_exe}")
    return out_exe


def create_remote_scratch(host: str, nonce: str, timeout: float) -> str:
    script = (
        "$ErrorActionPreference = 'Stop'\n"
        f"$base = Join-Path $env:TEMP ('ws-tunnel-smoke-' + {ps_quote(nonce)})\n"
        "New-Item -ItemType Directory -Path (Join-Path $base 'cache'), (Join-Path $base 'config') -Force | Out-Null\n"
        "Write-Output $base\n"
    )
    proc = run_ssh_powershell(host, script, timeout=timeout)
    lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if not lines:
        raise SmokeError("remote scratch creation returned empty path")
    return lines[-1]


def scp_to_remote(local_exe: Path, host: str, remote_scratch: str, timeout: float) -> str:
    remote_exe = remote_scratch.rstrip("\\/") + "\\ws-mcp.exe"
    remote_scp_target = remote_exe.replace("\\", "/")
    cmd = ["scp", *SSH_BASE_OPTS, str(local_exe), f"{host}:{remote_scp_target}"]
    proc = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=max(timeout, 30.0),
        check=False,
    )
    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        raise SmokeError(f"scp to Windows host failed (exit {proc.returncode}): {detail}")
    return remote_exe


def launch_remote_daemon(
    host: str,
    remote_exe: str,
    remote_scratch: str,
    remote_port: int,
    timeout: float,
) -> int:
    cache_dir = remote_scratch.rstrip("\\/") + "\\cache"
    config_dir = remote_scratch.rstrip("\\/") + "\\config"
    stdout_log = remote_scratch.rstrip("\\/") + "\\daemon-stdout.log"
    stderr_log = remote_scratch.rstrip("\\/") + "\\daemon-stderr.log"
    pid_file = remote_scratch.rstrip("\\/") + "\\daemon.pid"
    launch_err = remote_scratch.rstrip("\\/") + "\\launch-error.log"
    launch_ps1 = remote_scratch.rstrip("\\/") + "\\launch.ps1"

    # Write a small launcher script inside the scratch dir and invoke it via
    # Win32_Process.Create so ws-mcp.exe breaks out of OpenSSH's Win32 Job Object
    # while keeping the outer SSH -EncodedCommand well under cmd.exe's 8191-char limit.
    inner_lines = [
        "$ErrorActionPreference = 'Stop'",
        "try {",
        f"  $env:WS_CACHE_HOME = {ps_quote(cache_dir)}",
        f"  $env:WS_CONFIG_HOME = {ps_quote(config_dir)}",
        f"  $p = Start-Process -FilePath {ps_quote(remote_exe)} "
        f"-ArgumentList @('dashboard', '--port', {ps_quote(str(remote_port))}) "
        f"-WorkingDirectory {ps_quote(remote_scratch)} "
        "-WindowStyle Hidden "
        f"-RedirectStandardOutput {ps_quote(stdout_log)} "
        f"-RedirectStandardError {ps_quote(stderr_log)} "
        "-PassThru",
        f"  [System.IO.File]::WriteAllText({ps_quote(pid_file)}, [string]$p.Id)",
        "} catch {",
        f"  [System.IO.File]::WriteAllText({ps_quote(launch_err)}, ($_ | Out-String))",
        "  exit 1",
        "}",
    ]
    inner_ps_literal = ps_quote("\n".join(inner_lines) + "\n")
    launch_cmd = f'powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "{launch_ps1}"'
    outer_script = (
        "$ErrorActionPreference = 'Stop'\n"
        f"[System.IO.File]::WriteAllText({ps_quote(launch_ps1)}, {inner_ps_literal})\n"
        "$res = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{\n"
        f"  CommandLine = {ps_quote(launch_cmd)}\n"
        "}\n"
        "if ($res.ReturnValue -ne 0) {\n"
        "  throw ('Win32_Process.Create failed with ReturnValue=' + $res.ReturnValue)\n"
        "}\n"
        "$deadline = (Get-Date).AddSeconds(10)\n"
        "while ((Get-Date) -lt $deadline) {\n"
        f"  if (Test-Path -LiteralPath {ps_quote(launch_err)}) {{\n"
        f"    throw (Get-Content -LiteralPath {ps_quote(launch_err)} -Raw)\n"
        "  }\n"
        f"  if (Test-Path -LiteralPath {ps_quote(pid_file)}) {{\n"
        f"    $pidText = (Get-Content -LiteralPath {ps_quote(pid_file)} -Raw).Trim()\n"
        "    if ($pidText -match '^\\d+$') {\n"
        "      Write-Output $pidText\n"
        "      exit 0\n"
        "    }\n"
        "  }\n"
        "  Start-Sleep -Milliseconds 100\n"
        "}\n"
        "throw 'timed out waiting for detached daemon PID file'\n"
    )
    proc = run_ssh_powershell(host, outer_script, timeout=max(timeout, 15.0))
    lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if not lines or not lines[-1].isdigit():
        raise SmokeError(f"failed to parse remote daemon PID from output: {proc.stdout!r}")
    return int(lines[-1])


def fetch_remote_startup_diag(host: str, remote_scratch: str) -> str:
    logs = (
        ("stderr", remote_scratch.rstrip("\\/") + "\\daemon-stderr.log"),
        ("stdout", remote_scratch.rstrip("\\/") + "\\daemon-stdout.log"),
        ("dashboard.log", remote_scratch.rstrip("\\/") + "\\cache\\dashboard\\dashboard.log"),
    )
    parts = ["$ErrorActionPreference = 'SilentlyContinue'"]
    for label, path in logs:
        parts.append(
            f"if (Test-Path -LiteralPath {ps_quote(path)}) {{\n"
            f"  $c = (Get-Content -LiteralPath {ps_quote(path)} -Raw)\n"
            "  if ($c -and $c.Trim()) {\n"
            f"    Write-Output ('[{0}] ' + $c.Trim())\n"
            "  }\n"
            "}"
        )
    try:
        proc = run_ssh_powershell(host, "\n".join(parts), timeout=8.0, check=False)
        return (proc.stdout or "").strip()
    except (OSError, subprocess.SubprocessError):
        return ""


def start_ssh_tunnel(host: str, local_port: int, remote_port: int) -> subprocess.Popen[str]:
    cmd = [
        "ssh",
        "-N",
        *SSH_BASE_OPTS,
        "-o", "ExitOnForwardFailure=yes",
        "-L", f"127.0.0.1:{local_port}:127.0.0.1:{remote_port}",
        host,
    ]
    return subprocess.Popen(
        cmd,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        encoding="utf-8",
        errors="replace",
    )


def http_request(
    url: str,
    *,
    method: str = "GET",
    headers: dict[str, str] | None = None,
    body: bytes | None = None,
    timeout: float = 5.0,
) -> tuple[int, dict[str, str], bytes]:
    req = urllib.request.Request(url, data=body, headers=headers or {}, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            hdrs = {k.lower(): v for k, v in resp.headers.items()}
            return int(resp.status), hdrs, resp.read()
    except urllib.error.HTTPError as err:
        hdrs = {k.lower(): v for k, v in err.headers.items()} if err.headers else {}
        return int(err.code), hdrs, err.read()


def wait_for_health(
    local_port: int,
    expected_pid: int,
    tunnel_proc: subprocess.Popen[str],
    timeout: float,
) -> dict[str, object]:
    url = f"http://127.0.0.1:{local_port}/health"
    deadline = time.monotonic() + timeout
    last_err = ""
    while time.monotonic() < deadline:
        if tunnel_proc.poll() is not None:
            stderr = ""
            if tunnel_proc.stderr:
                stderr = tunnel_proc.stderr.read().strip()
            raise SmokeError(f"SSH tunnel exited early ({tunnel_proc.returncode}): {stderr}")
        try:
            status, _, data = http_request(url, timeout=min(2.0, timeout))
            if status == 200:
                payload = json.loads(data.decode("utf-8"))
                pid = payload.get("pid")
                if pid != expected_pid:
                    raise SmokeError(
                        f"/health PID mismatch: expected {expected_pid}, got {pid}"
                    )
                return payload
            last_err = f"HTTP {status}"
        except (OSError, urllib.error.URLError, json.JSONDecodeError) as exc:
            last_err = str(exc)
        time.sleep(0.2)
    raise SmokeError(f"timed out waiting for {url} after {timeout:.1f}s ({last_err})")


def fetch_remote_endpoint(host: str, remote_scratch: str, timeout: float) -> dict[str, object]:
    ep_path = remote_scratch.rstrip("\\/") + "\\cache\\dashboard\\endpoint.json"
    script = (
        "$ErrorActionPreference = 'Stop'\n"
        f"$p = {ps_quote(ep_path)}\n"
        "if (-not (Test-Path -LiteralPath $p)) { throw 'endpoint.json missing' }\n"
        "Get-Content -LiteralPath $p -Raw -Encoding UTF8\n"
    )
    deadline = time.monotonic() + min(timeout, 10.0)
    last_err = ""
    while time.monotonic() < deadline:
        proc = run_ssh_powershell(host, script, timeout=min(timeout, 10.0), check=False)
        if proc.returncode == 0 and proc.stdout.strip():
            data = json.loads(proc.stdout)
            if not isinstance(data, dict) or not data.get("token"):
                raise SmokeError("remote endpoint.json is missing a non-empty token field")
            return data
        last_err = (proc.stderr or proc.stdout or "").strip()
        time.sleep(0.2)
    raise SmokeError(f"failed to fetch remote endpoint.json: {last_err}")


def rewrite_url_to_local_port(raw_url: str, local_port: int) -> str:
    parsed = urllib.parse.urlparse(raw_url)
    return urllib.parse.urlunparse((
        parsed.scheme or "http",
        f"127.0.0.1:{local_port}",
        parsed.path or "/",
        parsed.params,
        parsed.query,
        parsed.fragment,
    ))


def mint_login_link(local_port: int, token: str, path: str = "/", timeout: float = 5.0) -> str:
    url = f"http://127.0.0.1:{local_port}/auth/link"
    status, _, data = http_request(
        url,
        method="POST",
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
        body=json.dumps({"path": path}).encode("utf-8"),
        timeout=timeout,
    )
    if status != 200:
        raise SmokeError(f"POST /auth/link failed with HTTP {status}")
    payload = json.loads(data.decode("utf-8"))
    raw_link = payload.get("url", "")
    if "?login=" not in raw_link and "&login=" not in raw_link:
        raise SmokeError("POST /auth/link response did not include a login query parameter")
    return rewrite_url_to_local_port(raw_link, local_port)


def verify_http_auth_and_pages(local_port: int, token: str, timeout: float) -> dict[str, object]:
    base = f"http://127.0.0.1:{local_port}"
    # 1. Confirm unauthenticated access is refused.
    unauth_status, _, _ = http_request(
        f"{base}/auth/status",
        headers={"Accept": "application/json"},
        timeout=min(timeout, 5.0),
    )
    if unauth_status != 401:
        raise SmokeError(f"expected HTTP 401 from unauthenticated /auth/status, got {unauth_status}")

    # 2. Verify POST /auth/cookie with Bearer token.
    cookie_status, _, cookie_raw = http_request(
        f"{base}/auth/cookie",
        method="POST",
        headers={"Authorization": f"Bearer {token}"},
        timeout=min(timeout, 5.0),
    )
    if cookie_status != 200:
        raise SmokeError(f"POST /auth/cookie failed with HTTP {cookie_status}")
    cookie_json = json.loads(cookie_raw.decode("utf-8"))
    cookie_name = cookie_json.get("name")
    cookie_value = cookie_json.get("value")
    if not cookie_name or not cookie_value:
        raise SmokeError("POST /auth/cookie response missing name/value")
    cookie_header = f"{cookie_name}={cookie_value}"

    # 3. Verify GET /, GET /settings, and GET /settings?frame=1 with the cookie.
    root_status, _, root_body = http_request(
        f"{base}/",
        headers={"Cookie": cookie_header, "Accept": "text/html"},
        timeout=min(timeout, 10.0),
    )
    root_html = root_body.decode("utf-8", errors="replace")
    if root_status != 200 or 'data-shell="1"' not in root_html or 'id="rail"' not in root_html:
        raise SmokeError(f"GET / failed dashboard shell check (HTTP {root_status})")

    settings_status, _, settings_body = http_request(
        f"{base}/settings",
        headers={"Cookie": cookie_header, "Accept": "text/html"},
        timeout=min(timeout, 10.0),
    )
    settings_html = settings_body.decode("utf-8", errors="replace")
    if settings_status != 200 or "workflow-settings" not in settings_html:
        raise SmokeError(f"GET /settings failed shell check (HTTP {settings_status})")

    settings_frame_status, _, settings_frame_body = http_request(
        f"{base}/settings?frame=1",
        headers={"Cookie": cookie_header, "Accept": "text/html"},
        timeout=min(timeout, 10.0),
    )
    settings_frame_html = settings_frame_body.decode("utf-8", errors="replace")
    if settings_frame_status != 200 or "data-settings-view" not in settings_frame_html:
        raise SmokeError(
            f"GET /settings?frame=1 failed settings view check (HTTP {settings_frame_status})"
        )

    # 4. Verify POST /auth/link mints a working single-use ?login=<code> URL.
    login_url = mint_login_link(local_port, token, path="/", timeout=min(timeout, 5.0))
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    req = urllib.request.Request(login_url, headers={"Accept": "text/html"})
    with opener.open(req, timeout=min(timeout, 10.0)) as resp:
        link_status = int(resp.status)
        link_html = resp.read().decode("utf-8", errors="replace")
    if link_status != 200 or 'data-shell="1"' not in link_html:
        raise SmokeError(f"GET ?login=<code> failed to land on dashboard shell (HTTP {link_status})")
    if not any(c.name == cookie_name for c in jar):
        raise SmokeError(f"GET ?login=<code> did not set {cookie_name} cookie")

    # Confirm the one-time code cannot be reused.
    replay_jar = http.cookiejar.CookieJar()
    replay_opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(replay_jar))
    try:
        with replay_opener.open(urllib.request.Request(login_url, headers={"Accept": "text/html"}), timeout=min(timeout, 5.0)) as resp:
            _ = resp.read()
    except urllib.error.HTTPError:
        pass
    if any(c.name == cookie_name for c in replay_jar):
        raise SmokeError("one-time ?login=<code> link remained valid on second use")

    return {
        "cookie_name": cookie_name,
        "root_status": root_status,
        "settings_status": settings_status,
        "settings_frame_status": settings_frame_status,
        "login_link_verified": True,
    }


def _parse_version_key(version: str) -> tuple[int, int, int, int]:
    match = re.match(r"^(\d+)\.(\d+)\.(\d+)(-.*)?$", (version or "").strip())
    if not match:
        return (0, 0, 0, 0)
    return (int(match.group(1)), int(match.group(2)), int(match.group(3)), 0 if match.group(4) else 1)


def find_playwright_index() -> Path:
    candidates: list[tuple[tuple[int, int, int, int], Path]] = []
    for env_key in ("WS_PLAYWRIGHT_MODULE", "BOARD_HEADLESS_PLAYWRIGHT", "PLAYWRIGHT_MODULE"):
        env_val = os.environ.get(env_key, "").strip()
        if not env_val:
            continue
        p = Path(env_val)
        index = p if p.name == "index.mjs" else p / "index.mjs"
        if index.is_file():
            return index
        raise SmokeError(f"{env_key}={env_val!r} does not contain index.mjs")

    npx_root = Path.home() / ".npm" / "_npx"
    if npx_root.is_dir():
        for entry in npx_root.iterdir():
            pw_dir = entry / "node_modules" / "playwright"
            index = pw_dir / "index.mjs"
            pkg = pw_dir / "package.json"
            if not (index.is_file() and pkg.is_file()):
                continue
            try:
                version = json.loads(pkg.read_text(encoding="utf-8")).get("version", "")
            except (OSError, json.JSONDecodeError):
                version = ""
            candidates.append((_parse_version_key(str(version)), index))
    if not candidates:
        raise SmokeError(
            "Playwright not found in WS_PLAYWRIGHT_MODULE or ~/.npm/_npx/*/node_modules/playwright; "
            "install with `npx playwright@latest install chromium` or pass --no-browser"
        )
    candidates.sort(key=lambda item: item[0], reverse=True)
    return candidates[0][1]


_PLAYWRIGHT_SCRIPT = r"""
import { pathToFileURL } from "node:url";

const pwIndex = process.env.PW_INDEX_MJS;
const loginUrl = process.env.SMOKE_LOGIN_URL;
const settingsUrl = process.env.SMOKE_SETTINGS_URL;
const timeoutMs = Number(process.env.SMOKE_TIMEOUT_MS || "20000");

const mod = await import(pathToFileURL(pwIndex).href);
const chromium = mod.chromium || (mod.default && mod.default.chromium);
if (!chromium) {
  throw new Error(`chromium export missing from ${pwIndex}`);
}

const browser = await chromium.launch({ headless: true });
try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();

  const pageErrors = [];
  const consoleErrors = [];
  const serverErrors = [];
  const authErrors = [];
  const wsEvents = [];

  page.on("pageerror", (err) => pageErrors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  page.on("response", (res) => {
    const status = res.status();
    if (status >= 500) serverErrors.push({ url: res.url(), status });
    if (status === 401 || status === 403) authErrors.push({ url: res.url(), status });
  });
  page.on("websocket", (ws) => {
    const rec = { url: ws.url(), framesReceived: 0, error: null, closed: false };
    wsEvents.push(rec);
    ws.on("framereceived", () => { rec.framesReceived += 1; });
    ws.on("socketerror", (err) => { rec.error = String(err); });
    ws.on("close", () => { rec.closed = true; });
  });

  await page.goto(loginUrl, { waitUntil: "load", timeout: timeoutMs });
  await page.waitForFunction(() => {
    const html = document.documentElement;
    const rail = document.getElementById("rail");
    const sse = document.getElementById("sse");
    const frame = document.querySelector("#frames iframe.sframe.on");
    let frameReady = false;
    try {
      frameReady = !!(frame && frame.contentDocument && frame.contentDocument.body && frame.contentDocument.body.innerHTML.trim().length > 0);
    } catch (_) {}
    return html.getAttribute("data-shell") === "1"
      && !!(rail && rail.innerHTML.trim().length > 0)
      && !!(sse && !sse.classList.contains("off"))
      && frameReady;
  }, null, { timeout: timeoutMs });

  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const railWs = wsEvents.find((w) => w.url.includes("/rail/events") && w.framesReceived > 0 && !w.error);
    if (railWs) break;
    await new Promise((r) => setTimeout(r, 100));
  }
  const homeWsCount = wsEvents.filter((w) => w.framesReceived > 0 && !w.error).length;
  if (homeWsCount === 0) {
    throw new Error(`no live WebSocket stream received frames on /: ${JSON.stringify(wsEvents)}`);
  }

  const homeSummary = await page.evaluate(() => ({
    url: location.pathname,
    railTextLen: (document.getElementById("rail")?.innerText || "").trim().length,
    sseOff: !!document.getElementById("sse")?.classList.contains("off"),
  }));

  await page.goto(settingsUrl, { waitUntil: "load", timeout: timeoutMs });
  await page.waitForFunction(() => {
    const dlg = document.getElementById("workflow-settings");
    const iframe = dlg && dlg.querySelector("iframe");
    if (!dlg || !dlg.hasAttribute("open") || !iframe) return false;
    try {
      const doc = iframe.contentDocument;
      const view = doc && doc.querySelector("[data-settings-view]");
      if (!view) return false;
      return view.querySelectorAll(".sgroup, .srow, .settings-nav button, section").length > 0;
    } catch (_) {
      return false;
    }
  }, null, { timeout: timeoutMs });

  const settingsSummary = await page.evaluate(() => {
    const iframe = document.querySelector("#workflow-settings iframe");
    const doc = iframe && iframe.contentDocument;
    const view = doc && doc.querySelector("[data-settings-view]");
    return {
      url: location.pathname,
      dialogOpen: !!document.getElementById("workflow-settings")?.hasAttribute("open"),
      settingsChildCount: view ? view.children.length : 0,
      sseOff: !!document.getElementById("sse")?.classList.contains("off"),
    };
  });

  const wsErrors = wsEvents.filter((w) => w.error);
  if (pageErrors.length || consoleErrors.length || serverErrors.length || authErrors.length || wsErrors.length) {
    throw new Error(JSON.stringify({
      pageErrors,
      consoleErrors,
      serverErrors,
      authErrors,
      wsErrors,
      wsEvents,
    }));
  }

  console.log(JSON.stringify({
    ok: true,
    home: homeSummary,
    settings: settingsSummary,
    wsStreams: wsEvents.map((w) => ({
      path: new URL(w.url).pathname,
      framesReceived: w.framesReceived,
    })),
  }));
} finally {
  await browser.close();
}
"""


def run_browser_smoke(local_port: int, token: str, timeout: float) -> dict[str, object]:
    node = shutil.which("node")
    if not node:
        raise SmokeError("node is not on PATH; cannot run Playwright browser check (or pass --no-browser)")
    pw_index = find_playwright_index()
    login_url = mint_login_link(local_port, token, path="/", timeout=min(timeout, 5.0))
    settings_url = f"http://127.0.0.1:{local_port}/settings"
    env = os.environ.copy()
    env["PW_INDEX_MJS"] = str(pw_index)
    env["SMOKE_LOGIN_URL"] = login_url
    env["SMOKE_SETTINGS_URL"] = settings_url
    env["SMOKE_TIMEOUT_MS"] = str(int(timeout * 1000))
    proc = subprocess.run(
        [node, "--input-type=module", "-e", _PLAYWRIGHT_SCRIPT],
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=timeout + 20.0,
        check=False,
    )
    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()
        raise SmokeError(f"Playwright browser smoke failed (exit {proc.returncode}): {detail}")
    lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if not lines:
        raise SmokeError("Playwright browser smoke produced no output")
    return json.loads(lines[-1])


def cleanup_smoke(ctx: SmokeContext) -> list[str]:
    if ctx.cleaned_up:
        return []
    ctx.cleaned_up = True
    warnings: list[str] = []

    # 1. Graceful shutdown via authenticated POST /shutdown over the tunnel.
    if ctx.token and ctx.tunnel_proc and ctx.tunnel_proc.poll() is None:
        try:
            status, _, _ = http_request(
                f"http://127.0.0.1:{ctx.local_port}/shutdown",
                method="POST",
                headers={"Authorization": f"Bearer {ctx.token}"},
                timeout=3.0,
            )
            if status not in (200, 202):
                warnings.append(f"POST /shutdown returned HTTP {status}")
        except (OSError, urllib.error.URLError) as exc:
            warnings.append(f"POST /shutdown failed: {exc}")

    # 2. Terminate local SSH tunnel process.
    if ctx.tunnel_proc is not None:
        if ctx.tunnel_proc.poll() is None:
            ctx.tunnel_proc.terminate()
            try:
                ctx.tunnel_proc.wait(timeout=5.0)
            except subprocess.TimeoutExpired:
                ctx.tunnel_proc.kill()
                ctx.tunnel_proc.wait(timeout=3.0)
        for stream in (ctx.tunnel_proc.stdout, ctx.tunnel_proc.stderr):
            if stream is not None:
                try:
                    stream.close()
                except OSError:
                    pass

    # 3. Over SSH: wait briefly for exact PID to exit, fallback to PID-scoped taskkill,
    #    then delete ONLY our scratch directory.
    if ctx.remote_pid is not None or ctx.remote_scratch:
        pid_part = ""
        if ctx.remote_pid is not None:
            pid_part = (
                f"$targetPid = {ctx.remote_pid}\n"
                "$deadline = (Get-Date).AddSeconds(5)\n"
                "while ((Get-Date) -lt $deadline) {\n"
                "  if (-not (Get-Process -Id $targetPid -ErrorAction SilentlyContinue)) { break }\n"
                "  Start-Sleep -Milliseconds 150\n"
                "}\n"
                "if (Get-Process -Id $targetPid -ErrorAction SilentlyContinue) {\n"
                "  & taskkill.exe /PID $targetPid /T /F | Out-Null\n"
                "  Start-Sleep -Milliseconds 250\n"
                "}\n"
            )
        scratch_part = ""
        if ctx.remote_scratch:
            scratch_part = (
                f"$scratch = {ps_quote(ctx.remote_scratch)}\n"
                "for ($i = 0; $i -lt 5; $i++) {\n"
                "  if (-not (Test-Path -LiteralPath $scratch)) { break }\n"
                "  try { Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction Stop; break }\n"
                "  catch { Start-Sleep -Milliseconds 250 }\n"
                "}\n"
            )
        try:
            proc = run_ssh_powershell(ctx.host, pid_part + scratch_part, timeout=20.0, check=False)
            if proc.returncode != 0:
                detail = (proc.stderr or proc.stdout or "").strip()
                warnings.append(f"remote cleanup exited {proc.returncode}: {detail}")
        except (OSError, subprocess.SubprocessError) as exc:
            warnings.append(f"remote cleanup error: {exc}")

    # 4. Remove local temp build dir.
    if ctx.local_tmp is not None:
        shutil.rmtree(ctx.local_tmp, ignore_errors=True)

    return warnings


def run_smoke(args: argparse.Namespace) -> dict[str, object]:
    local_port = args.port if args.port > 0 else pick_free_local_port()
    remote_port = pick_remote_port(args.port)
    ctx = SmokeContext(host=args.host, remote_port=remote_port, local_port=local_port)

    prev_sigterm = signal.getsignal(signal.SIGTERM)
    prev_sigint = signal.getsignal(signal.SIGINT)

    def _on_signal(signum: int, _frame: object) -> None:
        raise KeyboardInterrupt(f"signal {signum}")

    signal.signal(signal.SIGTERM, _on_signal)
    signal.signal(signal.SIGINT, _on_signal)

    try:
        t0 = time.monotonic()
        ctx.local_tmp = Path(tempfile.mkdtemp(prefix="ws-win-tunnel-smoke-"))
        if args.remote_binary:
            remote_exe = args.remote_binary
            ctx.timings["build_s"] = 0.0
        else:
            t_build = time.monotonic()
            local_exe = build_windows_binary(ctx.local_tmp)
            ctx.timings["build_s"] = round(time.monotonic() - t_build, 3)

        t_remote = time.monotonic()
        nonce = uuid.uuid4().hex[:12]
        ctx.remote_scratch = create_remote_scratch(ctx.host, nonce, args.timeout)
        if not args.remote_binary:
            remote_exe = scp_to_remote(local_exe, ctx.host, ctx.remote_scratch, args.timeout)
        ctx.remote_pid = launch_remote_daemon(
            ctx.host, remote_exe, ctx.remote_scratch, ctx.remote_port, args.timeout
        )
        ctx.tunnel_proc = start_ssh_tunnel(ctx.host, ctx.local_port, ctx.remote_port)
        try:
            health = wait_for_health(ctx.local_port, ctx.remote_pid, ctx.tunnel_proc, args.timeout)
        except SmokeError as exc:
            diag = fetch_remote_startup_diag(ctx.host, ctx.remote_scratch)
            if diag:
                raise SmokeError(f"{exc}\nremote logs:\n{diag}") from exc
            raise
        ctx.timings["startup_s"] = round(time.monotonic() - t_remote, 3)

        ep = fetch_remote_endpoint(ctx.host, ctx.remote_scratch, args.timeout)
        ctx.token = str(ep["token"])

        t_http = time.monotonic()
        http_summary = verify_http_auth_and_pages(ctx.local_port, ctx.token, args.timeout)
        ctx.timings["http_s"] = round(time.monotonic() - t_http, 3)

        browser_summary: dict[str, object] | None = None
        if not args.no_browser:
            t_browser = time.monotonic()
            browser_summary = run_browser_smoke(ctx.local_port, ctx.token, args.timeout)
            ctx.timings["browser_s"] = round(time.monotonic() - t_browser, 3)

        ctx.timings["total_s"] = round(time.monotonic() - t0, 3)
        summary: dict[str, object] = {
            "ok": True,
            "host": ctx.host,
            "local_port": ctx.local_port,
            "remote_port": health.get("port", ctx.remote_port),
            "remote_pid": ctx.remote_pid,
            "version": health.get("version"),
            "remote_scratch": ctx.remote_scratch,
            "http": http_summary,
            "browser": browser_summary if browser_summary is not None else "skipped",
            "timings": ctx.timings,
        }
        print(json.dumps(summary, indent=2))

        if args.keep_open:
            keep_login_url = mint_login_link(ctx.local_port, ctx.token, path="/", timeout=min(args.timeout, 5.0))
            print(f"\nTunnel open at: http://127.0.0.1:{ctx.local_port}/")
            print(f"One-time login: {keep_login_url}")
            print("Press Ctrl+C to shut down the remote scratch daemon and clean up.")
            while True:
                if ctx.tunnel_proc.poll() is not None:
                    raise SmokeError(f"SSH tunnel exited ({ctx.tunnel_proc.returncode})")
                time.sleep(0.5)

        return summary
    except KeyboardInterrupt:
        if args.keep_open:
            print("\nStopping tunneled Windows daemon and cleaning up...")
            return {"ok": True, "stopped_by": "signal"}
        raise
    finally:
        signal.signal(signal.SIGTERM, prev_sigterm)
        signal.signal(signal.SIGINT, prev_sigint)
        warnings = cleanup_smoke(ctx)
        for warning in warnings:
            print(f"warning: {warning}", file=sys.stderr)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        run_smoke(args)
        return 0
    except KeyboardInterrupt:
        return 130
    except (SmokeError, OSError, subprocess.SubprocessError, ValueError) as exc:
        print(f"win-tunnel-smoke: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
