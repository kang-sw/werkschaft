#!/usr/bin/env python3
"""Minimal local HTTP server for the Pure-VRM Companion Bust Spike.

Serves static spike source files from this directory, plus /vendor/* and
/samples/* from the untracked cache directory (/tmp/ws-vrm-spike by default),
keeping binary/npm artifacts out of the git repository.
"""

from __future__ import annotations

import argparse
import functools
import http.server
import mimetypes
import os
import posixpath
import signal
import sys
import urllib.parse
from pathlib import Path

SPIKE_DIR = Path(__file__).resolve().parent
DEFAULT_CACHE_DIR = Path("/tmp/ws-vrm-spike")

EXTRA_MIME_TYPES = {
    ".mjs": "application/javascript; charset=utf-8",
    ".js": "application/javascript; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".html": "text/html; charset=utf-8",
    ".json": "application/json; charset=utf-8",
    ".svg": "image/svg+xml",
    ".vrm": "model/gltf-binary",
    ".vrma": "model/gltf-binary",
    ".glb": "model/gltf-binary",
    ".wasm": "application/wasm",
}


class SpikeHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, cache_dir: Path, **kwargs):
        self.cache_dir = cache_dir
        super().__init__(*args, directory=str(SPIKE_DIR), **kwargs)

    def translate_path(self, path: str) -> str:
        parsed = urllib.parse.urlsplit(path).path
        clean = posixpath.normpath(urllib.parse.unquote(parsed))
        parts = [p for p in clean.split("/") if p and p not in (".", "..")]

        if parts and parts[0] in ("vendor", "samples"):
            cache_candidate = self.cache_dir.joinpath(*parts)
            if cache_candidate.exists():
                return str(cache_candidate)
            # Offline relative fallback inside spike directory
            local_candidate = SPIKE_DIR.joinpath(*parts)
            return str(local_candidate)

        if not parts:
            return str(SPIKE_DIR / "index.html")

        return str(SPIKE_DIR.joinpath(*parts))

    def guess_type(self, path: str) -> str:
        ext = Path(path).suffix.lower()
        if ext in EXTRA_MIME_TYPES:
            return EXTRA_MIME_TYPES[ext]
        guessed, _ = mimetypes.guess_type(path)
        return guessed or "application/octet-stream"

    def end_headers(self) -> None:
        self.send_header("Cache-Control", "no-store")
        self.send_header("Cross-Origin-Opener-Policy", "same-origin")
        self.send_header("X-Content-Type-Options", "nosniff")
        super().end_headers()

    def log_message(self, format: str, *args) -> None:
        if os.environ.get("SPIKE_QUIET") == "1":
            return
        super().log_message(format, *args)


class ReusableTCPServer(http.server.ThreadingHTTPServer):
    allow_reuse_address = True
    daemon_threads = True


def main() -> int:
    parser = argparse.ArgumentParser(description="Serve the Pure-VRM Companion Bust Spike.")
    parser.add_argument("--bind", default="127.0.0.1", help="Bind address (default: 127.0.0.1)")
    parser.add_argument("--port", type=int, default=8765, help="Port to listen on (default: 8765)")
    parser.add_argument(
        "--cache-dir",
        type=Path,
        default=DEFAULT_CACHE_DIR,
        help=f"Cache directory holding vendor/ and samples/ (default: {DEFAULT_CACHE_DIR})",
    )
    args = parser.parse_args()

    handler = functools.partial(SpikeHandler, cache_dir=args.cache_dir.resolve())
    server = ReusableTCPServer((args.bind, args.port), handler)

    def _handle_signal(_signum, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)

    url = f"http://{args.bind}:{args.port}/"
    print(f"Serving Pure-VRM Companion Bust Spike at {url}", flush=True)
    print(f"  Static root : {SPIKE_DIR}", flush=True)
    print(f"  Cache root  : {args.cache_dir.resolve()}", flush=True)

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
