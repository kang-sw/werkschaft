"""Unit tests for scripts/win-tunnel-smoke.py."""
from __future__ import annotations

import base64
import importlib.util
import io
import json
import os
from pathlib import Path
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


class HelpersTest(unittest.TestCase):
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


class OrchestrationAndSafetyTest(unittest.TestCase):
    def _mock_ssh_sequence(self, commands_seen: list[list[str]], secret_token: str):
        def fake_run(cmd, **kwargs):
            commands_seen.append(list(cmd))
            if cmd[0] == "ssh":
                encoded = cmd[cmd.index("-EncodedCommand") + 1]
                ps = base64.b64decode(encoded).decode("utf-16le")
                if "Join-Path $env:TEMP" in ps:
                    return mock.Mock(returncode=0, stdout="C:\\Temp\\ws-tunnel-smoke-abc123\n", stderr="")
                if "Win32_Process" in ps:
                    self.assertIn("Start-Process", ps)
                    self.assertIn("WS_CACHE_HOME", ps)
                    self.assertIn("WS_CONFIG_HOME", ps)
                    self.assertLess(len(encoded), 7000)
                    return mock.Mock(returncode=0, stdout="4242\n", stderr="")
                if "daemon-stderr.log" in ps:
                    return mock.Mock(returncode=0, stdout="", stderr="")
                if "endpoint.json" in ps:
                    return mock.Mock(
                        returncode=0,
                        stdout=json.dumps({"port": 39999, "pid": 4242, "token": secret_token}),
                        stderr="",
                    )
                if "Remove-Item" in ps:
                    self.assertNotIn("/IM", ps)
                    self.assertIn("4242", ps)
                    return mock.Mock(returncode=0, stdout="", stderr="")
            if Path(cmd[0]).name == "python3" or cmd[0] == sys.executable:
                return mock.Mock(returncode=0, stdout="", stderr="")
            if "go" in Path(cmd[0]).name:
                out_idx = cmd.index("-o") + 1
                Path(cmd[out_idx]).write_bytes(b"MZ-fake")
                return mock.Mock(returncode=0, stdout="", stderr="")
            if cmd[0] == "scp":
                return mock.Mock(returncode=0, stdout="", stderr="")
            if "node" in Path(cmd[0]).name:
                return mock.Mock(
                    returncode=0,
                    stdout=json.dumps({"ok": True, "home": {"railTextLen": 12}, "settings": {"dialogOpen": True}}),
                    stderr="",
                )
            raise AssertionError(f"unexpected subprocess command: {cmd}")
        return fake_run

    def test_full_mocked_smoke_redacts_token_and_cleans_up_safely(self):
        secret_token = "super-secret-endpoint-token-xyz"
        commands_seen: list[list[str]] = []
        tunnel_proc = mock.Mock()
        tunnel_proc.poll.return_value = None
        tunnel_proc.returncode = None
        tunnel_proc.stdout = io.StringIO("")
        tunnel_proc.stderr = io.StringIO("")

        http_calls: list[tuple[str, str]] = []

        def fake_http(url, *, method="GET", headers=None, body=None, timeout=5.0):
            http_calls.append((method, url))
            if url.endswith("/health"):
                return 200, {"content-type": "application/json"}, json.dumps({
                    "service": "ws-dashboard",
                    "version": "0.53.2-dev",
                    "pid": 4242,
                    "port": 39999,
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
                    "url": "http://127.0.0.1:39999/?login=oncecode1"
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
        with mock.patch.object(smoke.shutil, "which", side_effect=lambda name: f"/usr/bin/{name}"), \
             mock.patch.object(smoke.subprocess, "run", side_effect=self._mock_ssh_sequence(commands_seen, secret_token)), \
             mock.patch.object(smoke.subprocess, "Popen", return_value=tunnel_proc), \
             mock.patch.object(smoke, "http_request", side_effect=fake_http), \
             mock.patch.object(smoke.urllib.request, "build_opener", side_effect=fake_build_opener), \
             mock.patch.object(smoke, "find_playwright_index", return_value=pw_file), \
             mock.patch("sys.stdout", stdout_buf), \
             mock.patch("sys.stderr", stderr_buf):
            summary = smoke.run_smoke(args)

        self.assertTrue(summary["ok"])
        self.assertEqual(summary["remote_pid"], 4242)
        self.assertNotIn(secret_token, stdout_buf.getvalue())
        self.assertNotIn(secret_token, stderr_buf.getvalue())
        self.assertIn(("POST", "http://127.0.0.1:45123/shutdown"), http_calls)
        tunnel_proc.terminate.assert_called_once()
        # Verify no command ever runs taskkill /IM
        for cmd in commands_seen:
            joined = " ".join(cmd)
            self.assertNotIn("/IM", joined)

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
        # Teardown still ran remote cleanup for exact PID & scratch dir
        decoded_scripts = [
            base64.b64decode(cmd[cmd.index("-EncodedCommand") + 1]).decode("utf-16le")
            for cmd in commands_seen if cmd[0] == "ssh"
        ]
        self.assertTrue(any("Remove-Item" in s and "4242" in s for s in decoded_scripts))


if __name__ == "__main__":
    unittest.main()
