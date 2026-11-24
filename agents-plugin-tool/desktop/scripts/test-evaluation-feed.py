"""HTTP contracts for the Tauri-only evaluation feed."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from http.client import HTTPConnection
from http.server import ThreadingHTTPServer

spec = importlib.util.spec_from_file_location("feed", Path(__file__).with_name("evaluation-feed.py"))
feed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(feed)


class FeedTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.manifest = {"version": "2.0.0", "platforms": {}}
        for target, name in {
            "darwin-aarch64": "Werkschaft-darwin-arm64.app.tar.gz",
            "windows-x86_64-nsis": "Werkschaft-win32-x64-setup.exe",
            "windows-x86_64-msi": "Werkschaft-win32-x64.msi",
        }.items():
            (self.root / name).write_bytes(b"payload" * 1000)
            (self.root / (name + ".sig")).write_bytes(b"signature")
            self.manifest["platforms"][target] = {
                "url": f"https://github.com{feed.DOWNLOAD}v2.0.0/{name}",
                "signature": "signature", "sha256": "digest", "size": 7000,
            }
        (self.root / feed.MANIFEST).write_text(json.dumps(self.manifest))
        self.stable = feed.read_release(self.root, False)

    def request(self, path, fault=None):
        with ThreadingHTTPServer(("127.0.0.1", 0), feed.handler(self.stable, [], fault)) as server:
            worker = threading.Thread(target=server.handle_request)
            worker.start()
            connection = HTTPConnection(*server.server_address)
            connection.request("GET", path)
            response = connection.getresponse()
            try:
                body = response.read()
            except Exception as error:
                body = error.partial
            result = response.status, response.getheader("Content-Type"), response.getheader("Content-Length"), body
            connection.close()
            worker.join()
            return result

    def test_stable_cli_starts_without_migration_files(self):
        self.assertEqual(len(list(self.root.iterdir())), 7)
        process = subprocess.Popen([sys.executable, "-u", str(Path(feed.__file__)), "--stable", str(self.root)],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            self.assertIn("Serving the Werkschaft evaluation feed", process.stdout.readline())
            self.assertIsNone(process.poll())
        finally:
            process.terminate()
            process.communicate(timeout=5)

    def test_http_content_and_faults(self):
        for endpoint in (feed.RELEASES, feed.RELEASES + "/latest"):
            status, content, length, body = self.request(endpoint)
            self.assertEqual((status, content), (200, "application/json"))
            self.assertEqual(int(length), len(body))
            self.assertTrue(json.loads(body))
        prefix = feed.DOWNLOAD + "v2.0.0/"
        status, content, _, body = self.request(prefix + feed.MANIFEST)
        self.assertEqual((status, content), (200, "application/json"))
        self.assertEqual(json.loads(body), self.manifest)
        name = "Werkschaft-win32-x64-setup.exe"
        for suffix, expected in (("", b"payload" * 1000), (".sig", b"signature")):
            status, content, length, body = self.request(prefix + name + suffix)
            self.assertEqual((status, content), (200, "application/octet-stream"))
            self.assertEqual(int(length), len(expected))
            self.assertEqual(body, expected)
        for fault in ("missing-sha", "bad-signature"):
            entries = json.loads(self.request(prefix + feed.MANIFEST, fault)[3])["platforms"]
            for entry in entries.values():
                if fault == "missing-sha":
                    self.assertNotIn("sha256", entry)
                else:
                    self.assertEqual(entry["signature"], "invalid")
        self.assertNotEqual(self.request(prefix + name, "bad-bytes")[3], b"payload" * 1000)
        self.assertEqual(len(self.request(prefix + name, "interrupted")[3]), 4096)
        self.assertEqual(self.request(prefix + "missing")[0], 404)
        self.assertEqual(self.request("/unknown")[0], 404)
        self.assertEqual(self.request(feed.DOWNLOAD + "v9.0.0/" + name)[0], 404)
        self.assertEqual(self.stable[1], self.manifest)

    def test_channel_and_missing_asset_validation(self):
        with self.assertRaises(ValueError):
            feed.read_release(self.root, True)
        (self.root / "Werkschaft-win32-x64.msi.sig").unlink()
        with self.assertRaises(ValueError):
            feed.read_release(self.root, False)


if __name__ == "__main__":
    unittest.main()
