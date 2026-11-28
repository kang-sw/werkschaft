"""Unit tests for scripts/win-tunnel-smoke.py."""
from __future__ import annotations

import base64
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

SCRIPT_PATH = Path(__file__).resolve().parent / "win-tunnel-smoke.py"
SPEC = importlib.util.spec_from_file_location("win_tunnel_smoke", SCRIPT_PATH)
assert SPEC is not None and SPEC.loader is not None
smoke = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = smoke
SPEC.loader.exec_module(smoke)


def _decode_ssh_ps(cmd: list[str]) -> str:
    encoded = cmd[cmd.index("-EncodedCommand") + 1]
    return base64.b64decode(encoded).decode("utf-16le")


class ParseArgsTest(unittest.TestCase):
    def test_requires_host_when_env_missing(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            buf = io.StringIO()
            with mock.patch("sys.stderr", buf):
                with self.assertRaises(SystemExit) as ctx:
                    smoke.parse_args([])
            self.assertEqual(ctx.exception.code, 2)
            self.assertIn("WS_WIN_HOST", buf.getvalue())

    def test_uses_env_host_and_cli_override(self):
        with mock.patch.dict(os.environ, {"WS_WIN_HOST": "alice@win-box"}, clear=True):
            args = smoke.parse_args(["--no-browser", "--timeout", "15"])
            self.assertEqual(args.host, "alice@win-box")
            self.assertTrue(args.no_browser)
            self.assertEqual(args.timeout, 15.0)

            overridden = smoke.parse_args(["--host", "bob@other-box", "--port", "39417", "--keep-open"])
            self.assertEqual(overridden.host, "bob@other-box")
            self.assertEqual(overridden.port, 39417)
            self.assertTrue(overridden.keep_open)

    def test_rejects_option_injection_or_whitespace_in_host(self):
        for bad_host in ("-oProxyCommand=evil", "-Jjump", "user@host extra", "user@host\nline"):
            with self.subTest(bad_host=bad_host):
                buf = io.StringIO()
                with mock.patch("sys.stderr", buf):
                    with self.assertRaises(SystemExit) as ctx:
                        smoke.parse_args(["--host", bad_host])
                self.assertEqual(ctx.exception.code, 2)

    def test_rejects_nonfinite_timeout_and_empty_remote_binary(self):
        for argv in (
            ["--host", "alice@win", "--timeout", "nan"],
            ["--host", "alice@win", "--timeout", "inf"],
            ["--host", "alice@win", "--timeout", "0"],
            ["--host", "alice@win", "--remote-binary", "   "],
        ):
            with self.subTest(argv=argv):
                buf = io.StringIO()
                with mock.patch("sys.stderr", buf):
                    with self.assertRaises(SystemExit) as ctx:
                        smoke.parse_args(argv)
                self.assertEqual(ctx.exception.code, 2)


class HelpersTest(unittest.TestCase):
    def test_ps_quote_escapes_spaces_and_single_quotes(self):
        raw = "C:\\Program Files\\O'Brien\\ws-mcp.exe"
        self.assertEqual(smoke.ps_quote(raw), "'C:\\Program Files\\O''Brien\\ws-mcp.exe'")

    def test_encode_powershell_is_utf16le_base64(self):
        script = "Write-Output 'ok'"
        encoded = smoke.encode_powershell(script)
        decoded = base64.b64decode(encoded).decode("utf-16le")
        self.assertEqual(decoded, script)

    def test_rewrite_url_to_local_port(self):
        rewritten = smoke.rewrite_url_to_local_port(
            "http://127.0.0.1:43123/?login=secretcode123",
            51234,
        )
        self.assertEqual(rewritten, "http://127.0.0.1:51234/?login=secretcode123")

    def test_missing_go_mentions_remote_binary(self):
        with mock.patch.object(smoke.shutil, "which", return_value=None):
            with tempfile.TemporaryDirectory() as td:
                with self.assertRaisesRegex(smoke.SmokeError, "--remote-binary"):
                    smoke.build_windows_binary(Path(td))

    def test_validate_scratch_path_enforces_prefix_and_nonce(self):
        valid = smoke.validate_scratch_path(
            "C:\\Users\\alice\\AppData\\Local\\Temp\\ws-tunnel-smoke-0123456789ab",
            expected_nonce="0123456789ab",
        )
        self.assertTrue(valid.endswith("ws-tunnel-smoke-0123456789ab"))
        for bad in ("C:\\", "C:\\Windows\\System32", "C:\\Temp\\other-dir"):
            with self.subTest(bad=bad):
                with self.assertRaises(smoke.SmokeError):
                    smoke.validate_scratch_path(bad)
        with self.assertRaises(smoke.SmokeError):
            smoke.validate_scratch_path("C:\\Temp\\ws-tunnel-smoke-aaaaaaaaaaaa", expected_nonce="bbbbbbbbbbbb")

    def test_find_playwright_index_prefers_env_then_newest_npx(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            env_pw = root / "env-pw"
            env_pw.mkdir()
            (env_pw / "index.mjs").write_text("export const chromium = {};\n", encoding="utf-8")
            with mock.patch.dict(os.environ, {"WS_PLAYWRIGHT_MODULE": str(env_pw)}, clear=True):
                self.assertEqual(smoke.find_playwright_index(), env_pw / "index.mjs")

            npx = root / ".npm" / "_npx"
            for slug, ver in (("old", "1.49.0"), ("new", "1.52.0"), ("rc", "1.52.0-alpha-1")):
                d = npx / slug / "node_modules" / "playwright"
                d.mkdir(parents=True)
                (d / "index.mjs").write_text("", encoding="utf-8")
                (d / "package.json").write_text(json.dumps({"version": ver}), encoding="utf-8")
            with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(Path, "home", return_value=root):
                self.assertEqual(
                    smoke.find_playwright_index(),
                    npx / "new" / "node_modules" / "playwright" / "index.mjs",
                )

    def test_fetch_remote_startup_diag_uses_literal_log_labels(self):
        seen_ps: list[str] = []

        def fake_run(cmd, **kwargs):
            seen_ps.append(_decode_ssh_ps(cmd))
            return mock.Mock(returncode=0, stdout="[stderr] boom\n", stderr="")

        with mock.patch.object(smoke.subprocess, "run", side_effect=fake_run):
            out = smoke.fetch_remote_startup_diag("alice@win", "C:\\Temp\\ws-tunnel-smoke-1234567890ab")
        self.assertEqual(out, "[stderr] boom")
        self.assertEqual(len(seen_ps), 1)
        self.assertIn("'[stderr] '", seen_ps[0])
        self.assertIn("'[stdout] '", seen_ps[0])
        self.assertIn("'[dashboard.log] '", seen_ps[0])
        self.assertNotIn("[0]", seen_ps[0])

    def test_wait_for_health_checks_service_and_pid(self):
        tunnel = mock.Mock()
        tunnel.poll.return_value = None
        with mock.patch.object(
            smoke,
            "http_request",
            return_value=(200, {}, json.dumps({"service": "other", "pid": 4242}).encode("utf-8")),
        ):
            with self.assertRaisesRegex(smoke.SmokeError, "service mismatch"):
                smoke.wait_for_health(50000, 4242, tunnel, 2.0)

        with mock.patch.object(
            smoke,
            "http_request",
            return_value=(200, {}, json.dumps({"service": "ws-dashboard", "pid": 9999}).encode("utf-8")),
        ):
            with self.assertRaisesRegex(smoke.SmokeError, "PID mismatch"):
                smoke.wait_for_health(50000, 4242, tunnel, 2.0)

    def test_playwright_script_redacts_login_token_in_errors(self):
        self.assertIn("sanitizeUrl", smoke._PLAYWRIGHT_SCRIPT)
        self.assertIn('u.searchParams.set("login", "REDACTED")', smoke._PLAYWRIGHT_SCRIPT)
        node = shutil.which("node")
        if not node:
            self.skipTest("node not on PATH")
        js = (
            'function sanitizeUrl(raw) { try { const u = new URL(raw); if (u.searchParams.has("login")) u.searchParams.set("login", "REDACTED"); return u.toString(); } catch (_) { return String(raw).replace(/([?&]login=)[^&#\\s"\']*/gi, "$1REDACTED"); } }\n'
            'function sanitizeText(raw) { return String(raw).replace(/([?&]login=)[^&#\\s"\']*/gi, "$1REDACTED"); }\n'
            'console.log(JSON.stringify({ u: sanitizeUrl("http://127.0.0.1:5000/?login=topsecret123&x=1"), t: sanitizeText("failed http://127.0.0.1:5000/?login=topsecret123") }));\n'
        )
        proc = subprocess.run([node, "--input-type=module", "-e", js], capture_output=True, text=True, check=True)
        out = json.loads(proc.stdout)
        self.assertNotIn("topsecret123", out["u"])
        self.assertNotIn("topsecret123", out["t"])
        self.assertIn("login=REDACTED", out["u"])
        self.assertIn("login=REDACTED", out["t"])


class OrchestrationAndSafetyTest(unittest.TestCase):
    def _mock_ssh_sequence(
        self,
        commands_seen: list[list[str]],
        secret_token: str,
        *,
        bound_port: int = 39999,
        pid: int = 4242,
        start_ticks: int = 133700000000000000,
    ):
        def fake_run(cmd, **kwargs):
            commands_seen.append(list(cmd))
            if cmd[0] == "ssh":
                ps = _decode_ssh_ps(cmd)
                self.assertIn("OutputEncoding", ps)
                if "Join-Path $env:TEMP" in ps:
                    m = __import__("re").search(r"ws-tunnel-smoke-' \+ '([0-9a-f]+)'", ps)
                    nonce = m.group(1) if m else "abc123"
                    return mock.Mock(returncode=0, stdout=f"C:\\Temp\\ws-tunnel-smoke-{nonce}\n", stderr="")
                if "Win32_Process" in ps and "Invoke-CimMethod" in ps:
                    self.assertIn("Start-Process", ps)
                    self.assertIn("WS_CACHE_HOME", ps)
                    self.assertIn("WS_CONFIG_HOME", ps)
                    self.assertIn("UTF8Encoding]::new($true)", ps)
                    self.assertLess(len(cmd[cmd.index("-EncodedCommand") + 1]), 7000)
                    return mock.Mock(returncode=0, stdout=f"{pid} {start_ticks}\n", stderr="")
                if "endpoint.json" in ps:
                    return mock.Mock(
                        returncode=0,
                        stdout=json.dumps({"port": bound_port, "pid": pid, "token": secret_token}),
                        stderr="",
                    )
                if "daemon-stderr.log" in ps:
                    return mock.Mock(returncode=0, stdout="", stderr="")
                if "Remove-Item" in ps:
                    self.assertNotIn("/IM", ps)
                    self.assertIn("Test-SmokeTargetProcess", ps)
                    return mock.Mock(returncode=0, stdout="", stderr="")
            if Path(cmd[0]).name == "python3" or cmd[0] == sys.executable:
                return mock.Mock(returncode=0, stdout="", stderr="")
            if "go" in Path(cmd[0]).name:
                out_idx = cmd.index("-o") + 1
                Path(cmd[out_idx]).write_bytes(b"MZ-fake")
                return mock.Mock(returncode=0, stdout="", stderr="")
            if cmd[0] == "scp":
                self.assertIn("--", cmd)
                return mock.Mock(returncode=0, stdout="", stderr="")
            if "node" in Path(cmd[0]).name:
                return mock.Mock(
                    returncode=0,
                    stdout=json.dumps({"ok": True, "home": {"railTextLen": 12}, "settings": {"dialogOpen": True}}),
                    stderr="",
                )
            raise AssertionError(f"unexpected subprocess command: {cmd}")
        return fake_run

    def test_full_mocked_smoke_uses_bound_port_and_double_dash_and_redacts_token(self):
        secret_token = "super-secret-endpoint-token-xyz"
        commands_seen: list[list[str]] = []
        popen_cmds: list[list[str]] = []
        popen_kwargs: list[dict[str, object]] = []
        tunnel_proc = mock.Mock()
        tunnel_proc.poll.return_value = None
        tunnel_proc.returncode = None
        tunnel_proc.stdout = io.StringIO("")
        tunnel_proc.stderr = io.StringIO("")

        def fake_popen(cmd, **kwargs):
            popen_cmds.append(list(cmd))
            popen_kwargs.append(dict(kwargs))
            return tunnel_proc

        http_calls: list[tuple[str, str]] = []

        def fake_http(url, *, method="GET", headers=None, body=None, timeout=5.0):
            http_calls.append((method, url))
            if url.endswith("/health"):
                return 200, {"content-type": "application/json"}, json.dumps({
                    "service": "ws-dashboard",
                    "version": "0.53.2-dev",
                    "pid": 4242,
                    "port": 49111,
                }).encode("utf-8")
            if url.endswith("/auth/status"):
                return 401, {"content-type": "application/json"}, b'{"error":"login_required"}'
            if url.endswith("/auth/cookie"):
                self.assertEqual((headers or {}).get("Authorization"), f"Bearer {secret_token}")
                return 200, {"content-type": "application/json"}, json.dumps({
                    "name": "ws_dashboard_localhost",
                    "value": "cookie-secret",
                    "maxAge": 34560000,
                }).encode("utf-8")
            if url.endswith("/"):
                return 200, {"content-type": "text/html"}, b'<html data-shell="1"><aside id="rail"></aside></html>'
            if url.endswith("/settings"):
                return 200, {"content-type": "text/html"}, b'<dialog id="workflow-settings"></dialog>'
            if url.endswith("/settings?frame=1"):
                return 200, {"content-type": "text/html"}, b'<main data-settings-view></main>'
            if url.endswith("/auth/link"):
                return 200, {"content-type": "application/json"}, json.dumps({
                    "url": "http://127.0.0.1:49111/?login=oncecode1"
                }).encode("utf-8")
            if url.endswith("/shutdown"):
                self.assertEqual((headers or {}).get("Authorization"), f"Bearer {secret_token}")
                return 202, {"content-type": "application/json"}, b'{"shutting_down":true}'
            raise AssertionError(f"unexpected HTTP call: {method} {url}")

        class FakeResponse:
            def __init__(self, status: int, body: bytes):
                self.status = status
                self._body = body
            def read(self):
                return self._body
            def __enter__(self):
                return self
            def __exit__(self, *args):
                return False

        opener_uses = {"count": 0}

        def fake_build_opener(processor):
            jar = processor.cookiejar
            opener = mock.Mock()
            def open_fn(req, timeout=5.0):
                opener_uses["count"] += 1
                if opener_uses["count"] == 1:
                    import http.cookiejar
                    jar.set_cookie(http.cookiejar.Cookie(
                        version=0, name="ws_dashboard_localhost", value="v",
                        port=None, port_specified=False,
                        domain="127.0.0.1", domain_specified=False, domain_initial_dot=False,
                        path="/", path_specified=True, secure=False, expires=None,
                        discard=True, comment=None, comment_url=None, rest={},
                    ))
                    return FakeResponse(200, b'<html data-shell="1"></html>')
                return FakeResponse(200, b'<html data-login-gate="1"></html>')
            opener.open.side_effect = open_fn
            return opener

        pw_file = Path("/tmp/fake-pw/index.mjs")
        stdout_buf = io.StringIO()
        stderr_buf = io.StringIO()
        args = smoke.parse_args(["--host", "tester@example-win", "--port", "45123"])
        prev_int = signal.getsignal(signal.SIGINT)
        prev_term = signal.getsignal(signal.SIGTERM)
        with mock.patch.object(smoke.shutil, "which", side_effect=lambda name: f"/usr/bin/{name}"), \
             mock.patch.object(smoke.subprocess, "run", side_effect=self._mock_ssh_sequence(commands_seen, secret_token, bound_port=49111)), \
             mock.patch.object(smoke.subprocess, "Popen", side_effect=fake_popen), \
             mock.patch.object(smoke, "http_request", side_effect=fake_http), \
             mock.patch.object(smoke.urllib.request, "build_opener", side_effect=fake_build_opener), \
             mock.patch.object(smoke, "find_playwright_index", return_value=pw_file), \
             mock.patch("sys.stdout", stdout_buf), \
             mock.patch("sys.stderr", stderr_buf):
            summary = smoke.run_smoke(args)

        self.assertEqual(signal.getsignal(signal.SIGINT), prev_int)
        self.assertEqual(signal.getsignal(signal.SIGTERM), prev_term)
        self.assertTrue(summary["ok"])
        self.assertEqual(summary["remote_pid"], 4242)
        self.assertEqual(summary["remote_port"], 49111)
        self.assertNotIn(secret_token, stdout_buf.getvalue())
        self.assertNotIn(secret_token, stderr_buf.getvalue())
        self.assertIn(("POST", "http://127.0.0.1:45123/shutdown"), http_calls)
        tunnel_proc.terminate.assert_called_once()
        # Verify tunnel used bound fallback port from endpoint.json, `--`, and start_new_session.
        self.assertEqual(len(popen_cmds), 1)
        self.assertIn("127.0.0.1:45123:127.0.0.1:49111", popen_cmds[0])
        self.assertEqual(popen_cmds[0][-2:], ["--", "tester@example-win"])
        self.assertTrue(popen_kwargs[0].get("start_new_session"))
        # Verify `--` on every ssh/scp invocation and zero taskkill /IM.
        for cmd in commands_seen:
            if cmd[0] == "ssh":
                self.assertEqual(cmd[cmd.index("--") + 1], "tester@example-win")
            if cmd[0] == "scp":
                self.assertIn("--", cmd)
            self.assertNotIn("/IM", " ".join(cmd))

    def test_remote_binary_skips_build_and_scp_and_cleans_up_on_health_failure(self):
        commands_seen: list[list[str]] = []
        tunnel_proc = mock.Mock()
        tunnel_proc.poll.return_value = 1
        tunnel_proc.returncode = 255
        tunnel_proc.stdout = io.StringIO("")
        tunnel_proc.stderr = io.StringIO("port forward failed")

        args = smoke.parse_args([
            "--host", "tester@example-win",
            "--remote-binary", "C:\\prebuilt\\ws-mcp.exe",
            "--no-browser",
        ])
        with mock.patch.object(smoke.subprocess, "run", side_effect=self._mock_ssh_sequence(commands_seen, "tok")), \
             mock.patch.object(smoke.subprocess, "Popen", return_value=tunnel_proc):
            with self.assertRaisesRegex(smoke.SmokeError, "SSH tunnel exited early"):
                smoke.run_smoke(args)

        self.assertFalse(any(cmd[0] == "scp" or "go" in Path(cmd[0]).name for cmd in commands_seen))
        decoded_scripts = [_decode_ssh_ps(cmd) for cmd in commands_seen if cmd[0] == "ssh"]
        self.assertTrue(any(
            "Remove-Item" in s and "4242" in s and "133700000000000000" in s and "Test-SmokeTargetProcess" in s
            for s in decoded_scripts
        ))

    def test_cleanup_smoke_reads_daemon_pid_fallback_and_ignores_tunnel_error(self):
        seen_scripts: list[str] = []

        def fake_run(cmd, **kwargs):
            seen_scripts.append(_decode_ssh_ps(cmd))
            return mock.Mock(returncode=0, stdout="", stderr="")

        tunnel_proc = mock.Mock()
        tunnel_proc.poll.return_value = None
        tunnel_proc.terminate.side_effect = OSError("already gone")
        tunnel_proc.stdout = None
        tunnel_proc.stderr = None

        ctx = smoke.SmokeContext(
            host="tester@example-win",
            remote_port=39999,
            local_port=50000,
            remote_scratch="C:\\Temp\\ws-tunnel-smoke-0123456789ab",
            remote_exe="C:\\Temp\\ws-tunnel-smoke-0123456789ab\\ws-mcp.exe",
            remote_pid=None,
            tunnel_proc=tunnel_proc,
        )
        with mock.patch.object(smoke.subprocess, "run", side_effect=fake_run):
            warnings = smoke.cleanup_smoke(ctx)

        self.assertTrue(any("tunnel cleanup error" in w for w in warnings))
        self.assertEqual(len(seen_scripts), 1)
        self.assertIn("daemon.pid", seen_scripts[0])
        self.assertIn("Test-SmokeTargetProcess", seen_scripts[0])
        self.assertIn("Remove-Item", seen_scripts[0])

    def test_cleanup_smoke_refuses_invalid_scratch_path_and_keeps_sig_ign_during_cleanup(self):
        called_ssh = {"count": 0}

        def fake_run(cmd, **kwargs):
            called_ssh["count"] += 1
            return mock.Mock(returncode=0, stdout="", stderr="")

        with tempfile.TemporaryDirectory() as td:
            local_tmp = Path(td) / "sub"
            local_tmp.mkdir()
            ctx = smoke.SmokeContext(
                host="tester@example-win",
                remote_port=39999,
                local_port=50000,
                remote_scratch="C:\\Temp\\not-a-smoke-dir",
                local_tmp=local_tmp,
            )
            with mock.patch.object(smoke.subprocess, "run", side_effect=fake_run):
                warnings = smoke.cleanup_smoke(ctx)
            self.assertEqual(called_ssh["count"], 0)
            self.assertTrue(any("unexpected remote scratch path" in w for w in warnings))
            self.assertFalse(local_tmp.exists())

        # Verify run_smoke sets SIG_IGN during cleanup_smoke and restores handlers afterwards.
        observed_handlers: list[tuple[object, object]] = []

        def fake_cleanup(c):
            observed_handlers.append((signal.getsignal(signal.SIGINT), signal.getsignal(signal.SIGTERM)))
            return []

        prev_int = signal.getsignal(signal.SIGINT)
        prev_term = signal.getsignal(signal.SIGTERM)
        args = smoke.parse_args(["--host", "tester@example-win", "--remote-binary", "C:\\ws-mcp.exe", "--no-browser"])
        with mock.patch.object(smoke, "create_remote_scratch", side_effect=smoke.SmokeError("boom")), \
             mock.patch.object(smoke, "cleanup_smoke", side_effect=fake_cleanup):
            with self.assertRaisesRegex(smoke.SmokeError, "boom"):
                smoke.run_smoke(args)
        self.assertEqual(observed_handlers, [(signal.SIG_IGN, signal.SIG_IGN)])
        self.assertEqual(signal.getsignal(signal.SIGINT), prev_int)
        self.assertEqual(signal.getsignal(signal.SIGTERM), prev_term)


if __name__ == "__main__":
    unittest.main()
