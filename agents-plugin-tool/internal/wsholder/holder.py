#!/usr/bin/env python3
"""ws session holder: owns one child process and outlives the dashboard daemon.

A holder is started detached by the dashboard daemon (one holder per hosted
agent process or terminal). It owns the child (a process over pipes, or a
shell over a PTY), keeps the child's output in a bounded ring, and serves one
attach connection at a time on a loopback TCP port. The daemon may exit and
restart; its successor reads the holder's descriptor file, attaches, and
replays the ring, so an in-flight turn or shell continues.

Standard library only, Python 3.10 or newer.

State directory (one per holder, mode 0700): <dir>/descriptor.json (0600)

    {"schema": 1, "id": "<holder id>", "pid": <holder pid>,
     "port": <loopback TCP port>, "token": "<hex>", "protocol": 1,
     "started_at": <wall-clock start, epoch seconds>,
     "start_ticks": <start in clock ticks since boot; Linux only>,
     "script_hash": "<sha256 of this script>", "restart_safe": true,
     "session": {...}}

The holder rewrites the descriptor when it goes missing (checked every 60 s).
On a graceful exit it leaves exit.json for the manager to consume and remove
with the state directory: {"exit_code": <int|null>, "signal": <int|null>,
"reason": "child_exited" | "stop" | "idle" | "fatal",
"stopped_at": <RFC3339 UTC timestamp>}. A holder started with
--exit-copy PATH also writes that same object to PATH (creating its parent
directories, mode 0700), a place the generic cleanup of the state directory
does not reach; a failure to write the copy is logged and does not stop the
exit.

Attach protocol, version 1. Newline-delimited JSON objects, UTF-8, both ways.

  holder -> client, on connect:  {"hello": "ws-holder", "protocol": 1}
  client -> holder, first line:  {"op": "attach", "id": <any>, "token": "...",
                                  "protocol": <version the client speaks>}
  holder -> client:  {"reply": <id>, "ok": true, "protocol": <spoken>,
                      "holder": {"id", "pid", "started_at", "script_hash",
                                 "restart_safe", "session"},
                      "child": <child status>, "first_seq": n, "next_seq": n}
      or {"reply": <id>, "ok": false, "error": "token" | "protocol" |
          "bad_request", "message": "..."} and the connection closes.

  The holder accepts the protocol version it reports and the one before it.

  Audit channel, an optional capability negotiated on attach (no protocol
  bump). A client that wants it adds "audit": <version it speaks> to its
  attach request; a holder that predates the channel ignores the field and
  never sends it. This holder speaks audit version 1: its attach reply then
  carries "audit": 1, and only then does it send the audit messages below
  on that connection. They are unsequenced, have no replay, and are never
  kept in the ring.

    {"event": "audit", "anchor": n, "index": k, "at": <UTC ns at read>,
     "data": "<base64>"}
        A whole stdout line the --ring-skip pattern kept out of the ring.
        anchor is the ring sequence of the last stdout line kept before it
        (0 before any), index its 1-based place among the lines skipped
        after that anchor, dropped ones counted.
    {"event": "audit_fence", "anchor": n, "next": m, "through": t,
     "tee_count_sent": s, "dropped_count": d}   plus "final": true at the
        end of the child's stdout. Sent at every kept-line batch boundary,
        after that interval's audit lines: the interval after anchor is
        closed, the kept lines next..through follow it with nothing skipped
        between them, and of the interval's lines s were sent and d dropped
        (no audit connection, or a full audit queue). A final fence has no
        next and closes the last interval. "lost_before": n, when present,
        counts lines dropped in earlier intervals whose fence reached no
        daemon. A holder without --ring-skip has nothing to tee and does not
        take the channel. Audit messages are written ahead of the ring's
        records, so a tee line and its fence may arrive before the kept line
        they follow.
  The newest authenticated attach takes over: the previous connection gets
  {"event": "detached", "reason": "replaced"} and is closed, and only the
  new one may send operations.

  Operations (each answered {"reply": <id>, "ok": true, ...} or
  {"reply": <id>, "ok": false, "error": "<code>", "message": "..."}):

    spawn   {"argv": [...], "cwd": "...", "env": {...}, "mode": "pipe" |
             "pty", "rows": 24, "cols": 80, "input": "<base64>",
             "input_eof": false} -> {"child": <status>}
            One child per holder. env, when given, is the whole environment.
            input and input_eof, both optional, are queued as one input
            operation's data and eof before the child's input writer runs,
            so they reach a child that exits at once and leave no window in
            which its stdin stays open; without them stdin stays open for
            later input operations. A bad input refuses the spawn before the
            child starts. A holder that predates these fields ignores them.
            "http2_capture": {"path": "...", "max_bytes": n}, optional, routes
            a pipe child's stderr lines that are Go HTTP/2 debug log lines
            ("http2: ...", after an optional log timestamp) to a filter and
            never to the ring: only framer summary lines ("Framer 0x..:
            wrote|read <TYPE>"), cut after their allowlisted fields
            (stream, len, flags, ErrCode, LastStreamID, incr), are appended
            to path with the holder's wall-clock time. max_bytes (64 MiB by
            default) bounds the whole file: lines stop where one stop marker
            line still fits, the marker is written once, and a file found
            at or over the cap, or already ending in the marker, gets
            nothing more. A line cut into several records is classified by
            its first record. A holder that predates the field ignores it.
    input   {"data": "<base64>", "eof": false}   eof closes a pipe child's
            stdin after the data.
    resize  {"rows": n, "cols": n}               PTY children only.
    signal  {"name": "INT" | "TERM" | "KILL" | "HUP" | "QUIT"}
    stop    {}   ends the child (TERM, then KILL after a grace period) and
            exits the holder.
    status  {} -> {"child": <status>, "first_seq": n, "next_seq": n}
    capture_note {"text": "..."} -> {"captured": bool}
            Appends one line of text (at most 512 bytes, line ends
            replaced) to the spawn's http2_capture file, under the same cap
            and with the holder's wall-clock time; captured is false when
            the child has no capture or the capture has stopped. The
            daemon marks a cutoff this way, so the capture file has a
            single writer. A holder that predates it answers unknown_op.
    replay  {"from": n} -> {"first_seq": n, "next_seq": n}
            Starts the record stream at sequence n; n <= 0 means the whole
            ring. When n has already left the ring the stream begins with
            {"event": "gap", "from": n, "to": <first held sequence>}.

  Child status: {"state": "none"} | {"state": "running", "pid", "mode"} |
  {"state": "exited", "pid", "mode", "exit_code", "signal"}. A running pipe
  child's status also carries "input_closed": whether an eof (spawn's
  input_eof or an input operation's) closed its stdin; an input operation
  after that is refused with "input_closed". A holder that predates the
  field sends none.

  Records (sequenced from 1, streamed after replay):

    {"event": "output", "seq": n, "stream": "stdout" | "stderr" | "pty",
     "data": "<base64>", "offset": n, "combined_offset": n}
                           plus "partial": true when a pipe line was cut at
                           the record limit and continues in the next record.
                           offset is the absolute byte offset of data in its
                           stream and combined_offset in all output, in
                           arrival order, so a replay that starts after
                           evicted records still places its bytes. A holder
                           that predates them sends neither.
    {"event": "exit", "seq": n, "exit_code": <int|null>, "signal": <int|null>,
     "stream_bytes": {"<stream>": n, ...}, "output_bytes": n}
                           stream_bytes and output_bytes are the output
                           totals before the exit, per stream and in all,
                           so a replay that holds no output record still
                           knows them. A holder that predates them sends
                           neither.

  The output ring retains 4 MiB by default. A holder started with
  --ring-ceiling-bytes can keep a backlog above --ring-bytes until it sends
  the records or reaches the ceiling and evicts the oldest records.

  A pipe child's output is one record per line (the newline included); a PTY
  child's output is one record per read. A holder started with --ring-skip
  keeps the pipe child's whole stdout lines that the pattern matches from
  their start out of the ring. With --ring-pulse it substitutes that
  content-free line at most once every five seconds while dropping matches.

Self-exit: once the child has exited (or none was ever spawned) and nobody
has been attached for the idle period (24 hours), the holder exits. A live
child never triggers it.
"""

import time

# The wall-clock start, taken before the remaining imports so it sits close
# to the OS creation time the daemon compares it with before a guarded kill.
STARTED_AT = time.time()

import argparse
import base64
import datetime
import hashlib
import hmac
import json
import os
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import threading

PROTOCOL_VERSION = 1
SCHEMA_VERSION = 1
DESCRIPTOR_NAME = "descriptor.json"
LOG_NAME = "holder.log"
EXIT_NAME = "exit.json"
RING_PULSE_SECONDS = 5.0
# The audit channel version this holder speaks (see the docstring).
AUDIT_VERSION = 1
# Audit messages one attachment may have queued: a tee line past either
# bound is dropped and counted in its interval's fence; a fence past the
# message bound is dropped too, and the daemon closes the interval on its
# own bounded wait.
AUDIT_QUEUE_MESSAGES = 4096
AUDIT_QUEUE_BYTES = 4 << 20


def _start_ticks():
    """This process's start in clock ticks since boot, where the OS has it.

    Linux only (/proc/self/stat, field 22). The daemon derives a Linux
    process's creation time from the boot clock, so the wall-clock start
    stops matching it once the wall clock steps; the ticks never change.
    """
    try:
        with open("/proc/self/stat", encoding="ascii", errors="replace") as f:
            text = f.read()
        # The command name, in parentheses, may hold spaces and parentheses.
        fields = text[text.rindex(")") + 1 :].split()
        return int(fields[22 - 3])
    except (OSError, ValueError, IndexError):
        return None


START_TICKS = _start_ticks() if sys.platform.startswith("linux") else None

RING_BYTES = 4 << 20
MAX_RECORD_BYTES = 1 << 20
IDLE_EXIT_SECONDS = 24 * 60 * 60.0
DESCRIPTOR_CHECK_SECONDS = 60.0
STOP_GRACE_SECONDS = 5.0
AUTH_TIMEOUT_SECONDS = 10.0
READ_CHUNK = 65536
# One request line: an input operation carries at most this much base64.
MAX_LINE_BYTES = 4 << 20
# Input the child has not read yet; beyond it an input operation is refused.
MAX_PENDING_INPUT = 8 << 20
# One writer pass sends at most this many records and bytes.
SEND_BATCH_RECORDS = 256
SEND_BATCH_BYTES = 256 << 10
# An HTTP/2 frame capture file stops at this size by default.
HTTP2_CAPTURE_MAX_BYTES = 64 << 20
# The head of one HTTP/2 trace line the filter looks at; the rest is dropped.
HTTP2_LINE_KEEP = 4096

WINDOWS = os.name == "nt"

SIGNAL_NAMES = ("INT", "TERM", "KILL", "HUP", "QUIT")


class OpError(Exception):
    """An operation's refusal: a short code and a message for a person."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


class Record:
    __slots__ = ("seq", "kind", "stream", "data", "partial", "fields", "offset", "combined_offset")

    def __init__(self, seq, kind, stream=None, data=b"", partial=False, fields=None):
        self.seq = seq
        self.kind = kind
        self.stream = stream
        self.data = data
        self.partial = partial
        self.fields = fields
        self.offset = None
        self.combined_offset = None

    def cost(self) -> int:
        return len(self.data) + 64

    def message(self) -> dict:
        if self.kind == "exit":
            msg = {"event": "exit", "seq": self.seq}
            msg.update(self.fields)
            return msg
        msg = {
            "event": "output",
            "seq": self.seq,
            "stream": self.stream,
            "data": base64.b64encode(self.data).decode("ascii"),
        }
        if self.partial:
            msg["partial"] = True
        if self.offset is not None:
            msg["offset"] = self.offset
            msg["combined_offset"] = self.combined_offset
        return msg


class Ring:
    """Sequenced records bounded by a byte budget; the newest always stays."""

    def __init__(self, capacity: int, ceiling: int | None = None) -> None:
        self.capacity = capacity
        self.ceiling = capacity if ceiling is None else max(capacity, ceiling)
        self.delivered_before = 0
        self.items: list[Record] = []
        self.head = 0
        self.size = 0
        self.next_seq = 1
        # Output bytes appended so far, per stream and in all: an output
        # record's offsets, which eviction never renumbers.
        self.stream_bytes: dict = {}
        self.output_bytes = 0

    def first_seq(self) -> int:
        if self.head < len(self.items):
            return self.items[self.head].seq
        return self.next_seq

    def append(self, kind, stream=None, data=b"", partial=False, fields=None) -> Record:
        rec = Record(self.next_seq, kind, stream, data, partial, fields)
        if kind == "output":
            rec.offset = self.stream_bytes.get(stream, 0)
            rec.combined_offset = self.output_bytes
            self.stream_bytes[stream] = rec.offset + len(data)
            self.output_bytes += len(data)
        self.next_seq += 1
        self.items.append(rec)
        self.size += rec.cost()
        self._trim()
        return rec

    def set_delivered_before(self, seq: int) -> None:
        """Reclaim sent records above the retained budget. Replay can rewind seq."""
        self.delivered_before = seq
        if self.size > self.capacity and self.first_seq() < seq:
            self._trim(compact_to_base=True)

    def _trim(self, compact_to_base: bool = False) -> None:
        while self.size > self.capacity and self.head < len(self.items) - 1:
            oldest = self.items[self.head]
            if self.size <= self.ceiling and oldest.seq >= self.delivered_before:
                break
            self.size -= oldest.cost()
            self.items[self.head] = None
            self.head += 1
        if (compact_to_base and self.size <= self.capacity) or (self.head > 1024 and self.head > len(self.items) // 2):
            self.items = self.items[self.head :]
            self.head = 0

    def batch(self, seq: int, max_records: int, max_bytes: int) -> list[Record]:
        """Records from seq on (seq is at least first_seq), bounded."""
        start = self.head + (seq - self.first_seq())
        out, total = [], 0
        for rec in self.items[start : start + max_records]:
            out.append(rec)
            total += rec.cost()
            if total >= max_bytes:
                break
        return out


def unskipped(pattern, pieces, at_start: bool):
    """The pieces of a pipe's output that pattern does not skip, and whether
    the next piece starts a line. Only a whole line (one record that starts
    a line and is not cut) is skipped."""
    marked, at_start = split_skipped(pattern, pieces, at_start)
    kept = [(data, partial) for keep, data, partial in marked if keep]
    return kept, at_start, len(kept) < len(marked)


def split_skipped(pattern, pieces, at_start: bool):
    """Each piece marked kept or skipped, in order, and whether the next
    piece starts a line. As in unskipped, only a whole line is skipped."""
    marked = []
    for data, partial in pieces:
        skip = bool(at_start and not partial and pattern.match(data))
        marked.append((not skip, data, partial))
        at_start = not partial
    return marked, at_start


# Go's HTTP/2 debug log lines (GODEBUG=http2debug), after the log package's
# optional date and time.
HTTP2_LOG_PREFIX = re.compile(rb"^(?:\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)? )?http2: ")
HTTP2_FRAMER = re.compile(
    rb"^(?:\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)? )?http2: "
    rb"(Framer 0x[0-9a-f]+: (?:wrote|read) [A-Z][A-Z0-9_]*)(?=[ \r\n]|$)"
)
HTTP2_FIELD = re.compile(rb"^(stream|len|flags|ErrCode|LastStreamID|incr)=([A-Za-z0-9_|]+),?$")


def http2_frame_summary(line: bytes):
    """The kept part of an HTTP/2 framer summary line, or None for every
    other line: the framer, direction, and frame type, then the leading
    allowlisted fields, cut at the first token that is not one of them
    (header values, data previews, settings, debug data)."""
    m = HTTP2_FRAMER.match(line)
    if not m:
        return None
    kept = [m.group(1)]
    for token in line[m.end(1) :].split():
        if token == b"(conn)":
            kept.append(token)
            continue
        field = HTTP2_FIELD.match(token)
        if not field:
            break
        kept.append(field.group(1) + b"=" + field.group(2))
    return b" ".join(kept)


HTTP2_CAPTURE_STOP = b"ws holder: frame capture stopped at its byte cap\n"
HTTP2_NOTE_MAX = 512


class Http2Capture:
    """An append-only frame capture file, opened on its first line and held
    open. The cap counts every byte, the stop marker's included: lines stop
    where the marker still fits, the marker (HTTP2_CAPTURE_STOP) is written
    once, and a file reopened at or over the cap, or already ending in the
    marker, gets nothing more."""

    def __init__(self, path: str, max_bytes: int, log) -> None:
        self.path = path
        self.max_bytes = max_bytes
        self.log = log
        self.fd = None
        self.size = 0
        self.done = False
        self.lock = threading.Lock()

    def write(self, kept: bytes) -> bool:
        """Appends kept with the wall-clock time; False when it was not
        written (the capture has stopped)."""
        with self.lock:
            if self.done:
                return False
            stamp = datetime.datetime.now().astimezone().isoformat(timespec="microseconds").encode("ascii")
            try:
                if self.fd is None and not self._open():
                    return False
                line = stamp + b" " + kept + b"\n"
                if self.size + len(line) + len(HTTP2_CAPTURE_STOP) > self.max_bytes:
                    if self.size + len(HTTP2_CAPTURE_STOP) <= self.max_bytes:
                        os.write(self.fd, HTTP2_CAPTURE_STOP)
                        self.size += len(HTTP2_CAPTURE_STOP)
                    self.done = True
                    return False
                os.write(self.fd, line)
                self.size += len(line)
                return True
            except OSError as exc:
                self.done = True
                self.log(f"http2 capture {self.path}: {exc}")
                return False

    def _open(self) -> bool:
        # O_BINARY keeps Windows from translating line ends, which would
        # break the byte count.
        flags = os.O_RDWR | os.O_APPEND | os.O_CREAT | getattr(os, "O_BINARY", 0)
        self.fd = os.open(self.path, flags, 0o600)
        self.size = os.fstat(self.fd).st_size
        if self.size >= self.max_bytes:
            self.done = True
            return False
        mark = len(HTTP2_CAPTURE_STOP)
        if self.size >= mark:
            # Appends still go to the end; the position only serves this
            # one read (os.pread is not on Windows).
            os.lseek(self.fd, self.size - mark, os.SEEK_SET)
            if os.read(self.fd, mark) == HTTP2_CAPTURE_STOP:
                self.done = True
                return False
        return True

    def close(self) -> None:
        with self.lock:
            self.done = True
            if self.fd is not None:
                try:
                    os.close(self.fd)
                except OSError:
                    pass
                self.fd = None


class Http2Router:
    """Splits one pipe stream's records into the ones the ring keeps and
    the HTTP/2 trace lines it never sees. A logical line is classified by
    its first record; every continuation record follows it."""

    def __init__(self, capture: Http2Capture) -> None:
        self.capture = capture
        self.at_start = True
        self.trace = False
        self.head = bytearray()

    def route(self, pieces):
        kept = []
        for data, partial in pieces:
            if self.at_start:
                self.trace = HTTP2_LOG_PREFIX.match(data) is not None
                self.head = bytearray()
            if self.trace:
                room = HTTP2_LINE_KEEP - len(self.head)
                if room > 0:
                    self.head += data[:room]
                if not partial:
                    summary = http2_frame_summary(bytes(self.head).rstrip(b"\r\n"))
                    self.head = bytearray()
                    if summary is not None:
                        self.capture.write(summary)
            else:
                kept.append((data, partial))
            self.at_start = not partial
        return kept


class LineSplitter:
    """Cuts a pipe's bytes into line records, each at most limit bytes."""

    def __init__(self, limit: int) -> None:
        self.limit = limit
        self.buf = bytearray()

    def feed(self, chunk: bytes):
        self.buf += chunk
        while True:
            nl = self.buf.find(b"\n")
            if 0 <= nl < self.limit:
                line = bytes(self.buf[: nl + 1])
                del self.buf[: nl + 1]
                yield line, False
            elif len(self.buf) >= self.limit:
                part = bytes(self.buf[: self.limit])
                del self.buf[: self.limit]
                yield part, True
            else:
                return

    def flush(self):
        if self.buf:
            rest = bytes(self.buf)
            self.buf.clear()
            yield rest, False


# --- children ---------------------------------------------------------------


def _exit_fields(returncode: int) -> dict:
    if not WINDOWS and returncode < 0:
        return {"exit_code": None, "signal": -returncode}
    return {"exit_code": returncode, "signal": None}


def _kill_tree_windows(pid: int) -> None:
    subprocess.run(
        ["taskkill", "/PID", str(pid), "/T", "/F"],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
        check=False,
    )


class PipeChild:
    """A child over three pipes. On POSIX it stays in the holder's process
    group, so a kill of that group (the daemon's guarded kill) ends it too."""

    mode = "pipe"

    def __init__(self, spec: dict) -> None:
        flags = subprocess.CREATE_NEW_PROCESS_GROUP if WINDOWS else 0
        self.proc = subprocess.Popen(
            spec["argv"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=spec["cwd"],
            env=spec["env"],
            bufsize=0,
            creationflags=flags,
        )
        self.pid = self.proc.pid

    def outputs(self):
        return [("stdout", self.proc.stdout.fileno()), ("stderr", self.proc.stderr.fileno())]

    def read(self, fd: int) -> bytes:
        try:
            return os.read(fd, READ_CHUNK)
        except OSError:
            return b""

    def write(self, data: bytes) -> None:
        view = memoryview(data)
        while view:
            n = self.proc.stdin.write(view)
            view = view[n if n else len(view) :]

    def close_input(self) -> None:
        try:
            self.proc.stdin.close()
        except OSError:
            pass

    def resize(self, rows: int, cols: int) -> None:
        raise OpError("not_pty", "the child runs over pipes, not a PTY")

    def signal(self, name: str) -> None:
        if WINDOWS:
            if name == "INT":
                os.kill(self.pid, signal.CTRL_BREAK_EVENT)
            else:
                _kill_tree_windows(self.pid)
            return
        os.kill(self.pid, getattr(signal, "SIG" + name))

    def wait(self) -> dict:
        return _exit_fields(self.proc.wait())

    def after_exit(self) -> None:
        pass

    def close(self) -> None:
        for f in (self.proc.stdin, self.proc.stdout, self.proc.stderr):
            try:
                f.close()
            except OSError:
                pass


# The PTY child starts through this trampoline: a fresh interpreter that is
# the session leader (start_new_session), takes the PTY as its controlling
# terminal, and execs the target. It keeps Python code out of the forked
# child of this threaded process, which preexec_fn would not.
_PTY_TRAMPOLINE = (
    "import fcntl,os,sys,termios\n"
    "fcntl.ioctl(0,termios.TIOCSCTTY,0)\n"
    "try:\n"
    " os.execvp(sys.argv[1],sys.argv[1:])\n"
    "except OSError as e:\n"
    " sys.stderr.write('exec failed: %s\\n'%e)\n"
    " os._exit(127)\n"
)


class PosixPtyChild:
    mode = "pty"

    def __init__(self, spec: dict) -> None:
        import pty

        env = spec["env"]
        path = (env if env is not None else os.environ).get("PATH")
        program = spec["argv"][0]
        if os.sep in program:
            base = spec["cwd"] or os.getcwd()
            if not os.access(os.path.join(base, program), os.X_OK):
                raise OSError(f"{program}: not an executable file")
        elif shutil.which(program, path=path) is None:
            raise OSError(f"{program}: not found on PATH")
        self.master, slave = pty.openpty()
        try:
            self._set_size(spec["rows"], spec["cols"])
            self.proc = subprocess.Popen(
                [sys.executable or "python3", "-I", "-c", _PTY_TRAMPOLINE, *spec["argv"]],
                stdin=slave,
                stdout=slave,
                stderr=slave,
                cwd=spec["cwd"],
                env=env,
                start_new_session=True,
            )
        except BaseException:
            os.close(self.master)
            raise
        finally:
            os.close(slave)
        self.pid = self.proc.pid

    def _set_size(self, rows: int, cols: int) -> None:
        import fcntl
        import struct
        import termios

        fcntl.ioctl(self.master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def outputs(self):
        return [("pty", self.master)]

    def read(self, fd: int) -> bytes:
        try:
            return os.read(fd, READ_CHUNK)
        except OSError:  # EIO once every slave end is closed
            return b""

    def write(self, data: bytes) -> None:
        view = memoryview(data)
        while view:
            view = view[os.write(self.master, view) :]

    def close_input(self) -> None:
        self.write(b"\x04")

    def resize(self, rows: int, cols: int) -> None:
        self._set_size(rows, cols)

    def signal(self, name: str) -> None:
        # The child is its session's leader, so its pid names its group.
        os.killpg(self.pid, getattr(signal, "SIG" + name))

    def wait(self) -> dict:
        return _exit_fields(self.proc.wait())

    def after_exit(self) -> None:
        pass

    def close(self) -> None:
        try:
            os.close(self.master)
        except OSError:
            pass


class ConPtyChild:
    """A child on a Windows pseudoconsole, through ctypes."""

    mode = "pty"

    def __init__(self, spec: dict) -> None:
        import ctypes
        from ctypes import wintypes

        self.ctypes = ctypes
        k32 = ctypes.WinDLL("kernel32", use_last_error=True)
        self.k32 = k32
        HANDLE, DWORD, BOOL = wintypes.HANDLE, wintypes.DWORD, wintypes.BOOL
        LPVOID, SIZE_T = ctypes.c_void_p, ctypes.c_size_t

        class COORD(ctypes.Structure):
            _fields_ = [("X", ctypes.c_short), ("Y", ctypes.c_short)]

        class STARTUPINFOW(ctypes.Structure):
            _fields_ = [
                ("cb", DWORD),
                ("lpReserved", wintypes.LPWSTR),
                ("lpDesktop", wintypes.LPWSTR),
                ("lpTitle", wintypes.LPWSTR),
                ("dwX", DWORD),
                ("dwY", DWORD),
                ("dwXSize", DWORD),
                ("dwYSize", DWORD),
                ("dwXCountChars", DWORD),
                ("dwYCountChars", DWORD),
                ("dwFillAttribute", DWORD),
                ("dwFlags", DWORD),
                ("wShowWindow", wintypes.WORD),
                ("cbReserved2", wintypes.WORD),
                ("lpReserved2", LPVOID),
                ("hStdInput", HANDLE),
                ("hStdOutput", HANDLE),
                ("hStdError", HANDLE),
            ]

        class STARTUPINFOEXW(ctypes.Structure):
            _fields_ = [("StartupInfo", STARTUPINFOW), ("lpAttributeList", LPVOID)]

        class PROCESS_INFORMATION(ctypes.Structure):
            _fields_ = [
                ("hProcess", HANDLE),
                ("hThread", HANDLE),
                ("dwProcessId", DWORD),
                ("dwThreadId", DWORD),
            ]

        self.COORD = COORD
        k32.CreatePipe.argtypes = [ctypes.POINTER(HANDLE), ctypes.POINTER(HANDLE), LPVOID, DWORD]
        k32.CreatePipe.restype = BOOL
        k32.CreatePseudoConsole.argtypes = [COORD, HANDLE, HANDLE, DWORD, ctypes.POINTER(HANDLE)]
        k32.CreatePseudoConsole.restype = ctypes.c_long
        k32.ResizePseudoConsole.argtypes = [HANDLE, COORD]
        k32.ResizePseudoConsole.restype = ctypes.c_long
        k32.ClosePseudoConsole.argtypes = [HANDLE]
        k32.ClosePseudoConsole.restype = None
        k32.InitializeProcThreadAttributeList.argtypes = [LPVOID, DWORD, DWORD, ctypes.POINTER(SIZE_T)]
        k32.InitializeProcThreadAttributeList.restype = BOOL
        k32.UpdateProcThreadAttribute.argtypes = [LPVOID, DWORD, SIZE_T, LPVOID, SIZE_T, LPVOID, LPVOID]
        k32.UpdateProcThreadAttribute.restype = BOOL
        k32.DeleteProcThreadAttributeList.argtypes = [LPVOID]
        k32.DeleteProcThreadAttributeList.restype = None
        k32.CreateProcessW.argtypes = [
            wintypes.LPCWSTR,
            wintypes.LPWSTR,
            LPVOID,
            LPVOID,
            BOOL,
            DWORD,
            LPVOID,
            wintypes.LPCWSTR,
            ctypes.POINTER(STARTUPINFOEXW),
            ctypes.POINTER(PROCESS_INFORMATION),
        ]
        k32.CreateProcessW.restype = BOOL
        k32.ReadFile.argtypes = [HANDLE, LPVOID, DWORD, ctypes.POINTER(DWORD), LPVOID]
        k32.ReadFile.restype = BOOL
        k32.WriteFile.argtypes = [HANDLE, LPVOID, DWORD, ctypes.POINTER(DWORD), LPVOID]
        k32.WriteFile.restype = BOOL
        k32.WaitForSingleObject.argtypes = [HANDLE, DWORD]
        k32.WaitForSingleObject.restype = DWORD
        k32.GetExitCodeProcess.argtypes = [HANDLE, ctypes.POINTER(DWORD)]
        k32.GetExitCodeProcess.restype = BOOL
        k32.TerminateProcess.argtypes = [HANDLE, ctypes.c_uint]
        k32.TerminateProcess.restype = BOOL
        k32.CloseHandle.argtypes = [HANDLE]
        k32.CloseHandle.restype = BOOL

        def pipe():
            read, write = HANDLE(), HANDLE()
            if not k32.CreatePipe(ctypes.byref(read), ctypes.byref(write), None, 0):
                raise ctypes.WinError(ctypes.get_last_error())
            return read, write

        self.lock = threading.Lock()
        self.closed = False
        in_read, self.in_write = pipe()
        self.out_read, out_write = pipe()
        self.hpc = HANDLE()
        hr = k32.CreatePseudoConsole(COORD(spec["cols"], spec["rows"]), in_read, out_write, 0, ctypes.byref(self.hpc))
        # The pseudoconsole holds its own duplicates of the two pipe ends.
        k32.CloseHandle(in_read)
        k32.CloseHandle(out_write)
        if hr != 0:
            k32.CloseHandle(self.in_write)
            k32.CloseHandle(self.out_read)
            raise OSError(f"CreatePseudoConsole failed: HRESULT {hr & 0xFFFFFFFF:#010x}")

        size = SIZE_T(0)
        k32.InitializeProcThreadAttributeList(None, 1, 0, ctypes.byref(size))
        self.attr_buf = ctypes.create_string_buffer(size.value)
        startup = STARTUPINFOEXW()
        startup.StartupInfo.cb = ctypes.sizeof(STARTUPINFOEXW)
        # STARTF_USESTDHANDLES with null handles: without it the child takes
        # the holder's own redirected standard handles instead of the
        # pseudoconsole's on Windows builds before 19H1.
        startup.StartupInfo.dwFlags = 0x00000100
        startup.lpAttributeList = ctypes.cast(self.attr_buf, LPVOID)
        info = PROCESS_INFORMATION()
        try:
            if not k32.InitializeProcThreadAttributeList(startup.lpAttributeList, 1, 0, ctypes.byref(size)):
                raise ctypes.WinError(ctypes.get_last_error())
            # PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE takes the handle itself.
            if not k32.UpdateProcThreadAttribute(
                startup.lpAttributeList, 0, 0x00020016, self.hpc, ctypes.sizeof(HANDLE), None, None
            ):
                raise ctypes.WinError(ctypes.get_last_error())
            env_block = None
            if spec["env"] is not None:
                env_block = ctypes.create_unicode_buffer(
                    "".join(f"{k}={v}\0" for k, v in spec["env"].items()) + "\0"
                )
            cmdline = ctypes.create_unicode_buffer(subprocess.list2cmdline(spec["argv"]))
            # EXTENDED_STARTUPINFO_PRESENT | CREATE_UNICODE_ENVIRONMENT
            if not k32.CreateProcessW(
                None, cmdline, None, None, False, 0x00080000 | 0x00000400, env_block, spec["cwd"],
                ctypes.byref(startup), ctypes.byref(info),
            ):
                raise ctypes.WinError(ctypes.get_last_error())
        except BaseException:
            k32.ClosePseudoConsole(self.hpc)
            k32.CloseHandle(self.in_write)
            k32.CloseHandle(self.out_read)
            raise
        finally:
            k32.DeleteProcThreadAttributeList(startup.lpAttributeList)
        k32.CloseHandle(info.hThread)
        self.process = info.hProcess
        self.pid = info.dwProcessId

    def outputs(self):
        return [("pty", self.out_read)]

    def read(self, handle) -> bytes:
        ctypes = self.ctypes
        buf = ctypes.create_string_buffer(READ_CHUNK)
        got = ctypes.c_ulong(0)
        if not self.k32.ReadFile(handle, buf, READ_CHUNK, ctypes.byref(got), None):
            return b""
        return buf.raw[: got.value]

    def write(self, data: bytes) -> None:
        ctypes = self.ctypes
        view = bytes(data)
        while view:
            wrote = ctypes.c_ulong(0)
            if not self.k32.WriteFile(self.in_write, view, len(view), ctypes.byref(wrote), None):
                raise ctypes.WinError(ctypes.get_last_error())
            view = view[wrote.value :]

    def close_input(self) -> None:
        self.write(b"\x1a\r")

    def resize(self, rows: int, cols: int) -> None:
        with self.lock:
            if not self.closed:
                self.k32.ResizePseudoConsole(self.hpc, self.COORD(cols, rows))

    def signal(self, name: str) -> None:
        if name == "INT":
            self.write(b"\x03")
        else:
            _kill_tree_windows(self.pid)

    def wait(self) -> dict:
        ctypes = self.ctypes
        self.k32.WaitForSingleObject(self.process, 0xFFFFFFFF)
        code = ctypes.c_ulong(0)
        self.k32.GetExitCodeProcess(self.process, ctypes.byref(code))
        return {"exit_code": code.value, "signal": None}

    def after_exit(self) -> None:
        # The output pipe reaches its end only once the pseudoconsole is
        # closed; the reader thread keeps draining meanwhile, which closing
        # needs on older Windows builds.
        with self.lock:
            if not self.closed:
                self.closed = True
                self.k32.ClosePseudoConsole(self.hpc)

    def close(self) -> None:
        self.after_exit()
        for handle in (self.in_write, self.out_read, self.process):
            self.k32.CloseHandle(handle)


def spawn_child(spec: dict):
    if spec["mode"] == "pipe":
        return PipeChild(spec)
    if WINDOWS:
        return ConPtyChild(spec)
    return PosixPtyChild(spec)


def parse_spawn_spec(req: dict) -> dict:
    argv = req.get("argv")
    if not isinstance(argv, list) or not argv or not all(isinstance(a, str) and a for a in argv[:1]):
        raise OpError("bad_request", "spawn needs a non-empty argv list of strings")
    if not all(isinstance(a, str) for a in argv):
        raise OpError("bad_request", "spawn needs a non-empty argv list of strings")
    cwd = req.get("cwd") or None
    if cwd is not None and not isinstance(cwd, str):
        raise OpError("bad_request", "spawn cwd must be a string")
    env = req.get("env")
    if env is not None and not (
        isinstance(env, dict) and all(isinstance(k, str) and isinstance(v, str) for k, v in env.items())
    ):
        raise OpError("bad_request", "spawn env must map strings to strings")
    mode = req.get("mode", "pipe")
    if mode not in ("pipe", "pty"):
        raise OpError("bad_request", 'spawn mode must be "pipe" or "pty"')
    rows, cols = parse_size(req, default=(24, 80))
    data = parse_input_data(req.get("input"))
    if len(data) > MAX_PENDING_INPUT:
        raise OpError("input_overflow", "the spawn input exceeds the unread input limit")
    capture = req.get("http2_capture")
    if capture is not None:
        if not isinstance(capture, dict) or not isinstance(capture.get("path"), str) or not capture["path"]:
            raise OpError("bad_request", "spawn http2_capture needs a path string")
        limit = capture.get("max_bytes", HTTP2_CAPTURE_MAX_BYTES)
        if isinstance(limit, bool) or not isinstance(limit, int) or limit <= 0:
            raise OpError("bad_request", "spawn http2_capture max_bytes must be a positive integer")
        capture = {"path": capture["path"], "max_bytes": limit}
    return {
        "http2_capture": capture,
        "argv": argv,
        "cwd": cwd,
        "env": env,
        "mode": mode,
        "rows": rows,
        "cols": cols,
        "input": data,
        "input_eof": req.get("input_eof") is True,
    }


def parse_input_data(value) -> bytes:
    """Decode an input operation's (or a spawn's) base64 data."""
    try:
        return base64.b64decode(value or "", validate=True)
    except (ValueError, TypeError):
        raise OpError("bad_request", "input data must be base64") from None


def parse_size(req: dict, default=None) -> tuple[int, int]:
    rows, cols = req.get("rows"), req.get("cols")
    if rows is None and cols is None and default is not None:
        return default
    for value in (rows, cols):
        if not isinstance(value, int) or isinstance(value, bool) or not 1 <= value <= 32767:
            raise OpError("bad_request", "rows and cols must be integers from 1 to 32767")
    return rows, cols


# --- the attach connection ---------------------------------------------------


class Attachment:
    """One authenticated connection. Its writer thread sends the queued
    control messages, then the ring's records from its cursor on; a client
    that falls behind the ring is told the gap, so nothing accumulates."""

    def __init__(self, holder: "Holder", sock: socket.socket) -> None:
        self.holder = holder
        self.sock = sock
        self.control: list[dict] = []
        self.streaming = False
        self.cursor = 0
        self.closing = False
        self.drain = False
        self.dead = False
        # The audit channel version negotiated on attach; 0 for none.
        self.audit = 0
        self.audit_queue: list[dict] = []
        self.audit_bytes = 0
        self.thread = threading.Thread(target=self._write_loop, daemon=True)

    # Callers hold holder.cond for push, start_stream, and finish.

    def push(self, message: dict) -> None:
        self.control.append(message)
        self.holder.cond.notify_all()

    def push_audit(self, message: dict, tee: bool) -> bool:
        """Queue an audit message; False when it is dropped (no audit channel,
        the connection closing, or the queue full)."""
        if not self.audit or self.closing or self.dead:
            return False
        size = len(message.get("data", "")) + 96
        if len(self.audit_queue) >= AUDIT_QUEUE_MESSAGES:
            return False
        if tee and self.audit_bytes + size > AUDIT_QUEUE_BYTES:
            return False
        self.audit_queue.append(message)
        self.audit_bytes += size
        self.holder.cond.notify_all()
        return True

    def start_stream(self, from_seq: int) -> None:
        ring = self.holder.ring
        if from_seq <= 0:
            from_seq = ring.first_seq()
        self.cursor = min(from_seq, ring.next_seq)
        ring.set_delivered_before(min(ring.delivered_before, self.cursor))
        self.streaming = True
        self.holder.cond.notify_all()

    def finish(self, drain: bool = False) -> None:
        """Send what is queued (with drain, the ring's remaining records
        too), then close."""
        self.closing = True
        self.drain = drain
        self.holder.cond.notify_all()
        timer = threading.Timer(5.0, self.kill)
        timer.daemon = True
        timer.start()

    def kill(self) -> None:
        self.dead = True
        try:
            self.sock.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        try:
            self.sock.close()
        except OSError:
            pass
        with self.holder.cond:
            self.holder.cond.notify_all()

    def _write_loop(self) -> None:
        holder = self.holder
        ring = holder.ring
        while True:
            with holder.cond:
                while True:
                    if self.dead:
                        return
                    pending = self.streaming and self.cursor < ring.next_seq and (self.drain or not self.closing)
                    if self.control or self.audit_queue or pending or self.closing:
                        break
                    holder.cond.wait()
                # Audit messages go after control replies and before the
                # ring's records: a fence reaches the client no later than
                # the kept lines it releases.
                messages = self.control + self.audit_queue
                self.control = []
                self.audit_queue = []
                self.audit_bytes = 0
                sent_before = 0
                if pending:
                    first = ring.first_seq()
                    if self.cursor < first:
                        messages.append({"event": "gap", "from": self.cursor, "to": first})
                        self.cursor = first
                    records = ring.batch(self.cursor, SEND_BATCH_RECORDS, SEND_BATCH_BYTES)
                    self.cursor += len(records)
                    if ring.ceiling > ring.capacity:
                        sent_before = self.cursor
                else:
                    records = []
                last = self.closing and not (self.drain and self.streaming and self.cursor < ring.next_seq)
            try:
                payload = b"".join(encode_line(m) for m in messages)
                payload += b"".join(encode_line(r.message()) for r in records)
                del records
                if payload:
                    self.sock.sendall(payload)
            except OSError:
                self.kill()
                return
            del payload
            if sent_before:
                with holder.cond:
                    if holder.attachment is self or self.drain:
                        # Replay can rewind the cursor while sendall is in flight.
                        ring.set_delivered_before(min(sent_before, self.cursor))
            if last:
                self.kill()
                return


def encode_line(message: dict) -> bytes:
    return json.dumps(message, separators=(",", ":")).encode("utf-8") + b"\n"


# --- the holder ---------------------------------------------------------------


class Holder:
    def __init__(
        self,
        directory: str,
        *,
        holder_id: str | None = None,
        session: dict | None = None,
        idle_exit: float = IDLE_EXIT_SECONDS,
        descriptor_check: float = DESCRIPTOR_CHECK_SECONDS,
        ring_bytes: int = RING_BYTES,
        ring_ceiling_bytes: int | None = None,
        ring_skip: str | None = None,
        ring_pulse: str | None = None,
        ring_pulse_seconds: float = RING_PULSE_SECONDS,
        stop_grace: float = STOP_GRACE_SECONDS,
        restart_safe: bool = True,
        script_path: str | None = None,
        exit_copy: str | None = None,
    ) -> None:
        self.dir = os.path.abspath(directory)
        self.id = holder_id or os.path.basename(self.dir)
        self.session = session if session is not None else {}
        self.idle_exit = idle_exit
        self.descriptor_check = descriptor_check
        self.stop_grace = stop_grace
        self.restart_safe = restart_safe
        self.exit_copy = os.path.abspath(exit_copy) if exit_copy else None
        self.token = secrets.token_hex(32)
        self.script_hash = _file_sha256(script_path or __file__)
        self.max_record = max(1, min(MAX_RECORD_BYTES, ring_bytes))
        self.ring = Ring(ring_bytes, ring_ceiling_bytes)
        self.ring_skip = re.compile(ring_skip.encode("utf-8")) if ring_skip else None
        self.ring_pulse = ring_pulse.encode("utf-8") + b"\n" if ring_pulse else None
        self.ring_pulse_seconds = ring_pulse_seconds
        self.ring_pulse_at = float("-inf")
        # The open audit interval of the skipping stdout reader: the last
        # kept stdout sequence, and the interval's skipped lines (index), sent
        # and dropped. Guarded by cond.
        self.tee_anchor = 0
        self.tee_index = 0
        self.tee_sent = 0
        self.tee_dropped = 0
        # Lines dropped in intervals whose fence reached no daemon; the next
        # delivered fence reports them as lost_before.
        self.tee_carry = 0
        # The first sequence of a kept run whose last record so far is a cut
        # line's head (partial): the run is fenced only once the line ends,
        # because a daemon maps whole lines and an anchor or a through on a
        # cut record names a sequence no line carries.
        self.tee_run_first = 0
        self.cond = threading.Condition()
        self.attachment: Attachment | None = None
        self.child = None
        self.http2_capture: Http2Capture | None = None
        self.child_status: dict = {"state": "none"}
        self.input_queue: list = []
        self.input_pending = 0
        self.idle_since = time.monotonic()
        self.stopping = False
        self.exit_event = threading.Event()
        # Prevents late log writes after shutdown has completed.
        self.log_lock = threading.Lock()
        self.log_closed = False
        self.exit_code = 0
        self.exit_reason = "stop"
        self.listener: socket.socket | None = None
        self.port = 0

    # -- lifecycle --

    def start(self) -> None:
        """Bind the attach port, write the descriptor, and start accepting."""
        self.listener = socket.create_server(("127.0.0.1", 0))
        self.port = self.listener.getsockname()[1]
        self.write_descriptor()
        self.log(f"holder {self.id} pid {os.getpid()} listening on 127.0.0.1:{self.port}")
        threading.Thread(target=self._accept_loop, daemon=True).start()

    def run(self) -> int:
        """Serve until a stop or the idle exit; returns the exit code."""
        self.start()
        tick = max(0.02, min(1.0, self.descriptor_check, self.idle_exit / 4 if self.idle_exit > 0 else 1.0))
        next_check = time.monotonic() + self.descriptor_check
        while not self.exit_event.wait(tick):
            now = time.monotonic()
            if now >= next_check:
                next_check = now + self.descriptor_check
                self.ensure_descriptor()
            with self.cond:
                idle = (
                    not self.stopping
                    and self.attachment is None
                    and self.child_status["state"] != "running"
                    and now - self.idle_since >= self.idle_exit
                )
            if idle:
                self.log("idle exit: no child running and nobody attached for the idle period")
                self.exit_reason = "idle"
                break
        self.shutdown()
        return self.exit_code

    def shutdown(self) -> None:
        """Close the port and attachment, leaving the exit record for the manager."""
        self.exit_event.set()
        if self.listener is not None:
            try:
                self.listener.close()
            except OSError:
                pass
        with self.cond:
            att, self.attachment = self.attachment, None
            if att is not None:
                att.finish(drain=True)
        if att is not None:
            att.thread.join(timeout=5.0)
        child = self.child
        if child is not None and self.child_status["state"] == "exited":
            child.close()
        self.write_exit_record()
        with self.log_lock:
            self.log_closed = True

    def write_exit_record(self) -> None:
        """Write exit.json and, with --exit-copy, its copy; each failure is
        logged on its own, so neither keeps the other or the exit from
        happening."""
        with self.cond:
            code = self.child_status.get("exit_code")
            child_signal = self.child_status.get("signal")
        record = {
            "exit_code": code,
            "signal": child_signal,
            "reason": self.exit_reason,
            "stopped_at": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
        }
        try:
            _write_json_file(os.path.join(self.dir, EXIT_NAME), record)
        except OSError as exc:
            self.log(f"exit record write failed: {exc}")
        if self.exit_copy:
            try:
                os.makedirs(os.path.dirname(self.exit_copy) or ".", mode=0o700, exist_ok=True)
                _write_json_file(self.exit_copy, record)
            except OSError as exc:
                self.log(f"exit record copy failed: {exc}")

    # -- descriptor --

    def descriptor(self) -> dict:
        doc = {
            "schema": SCHEMA_VERSION,
            "id": self.id,
            "pid": os.getpid(),
            "port": self.port,
            "token": self.token,
            "protocol": PROTOCOL_VERSION,
            "started_at": STARTED_AT,
            "script_hash": self.script_hash,
            "restart_safe": self.restart_safe,
            "session": self.session,
        }
        if START_TICKS:
            doc["start_ticks"] = START_TICKS
        return doc

    def write_descriptor(self) -> None:
        os.makedirs(self.dir, mode=0o700, exist_ok=True)
        path = os.path.join(self.dir, DESCRIPTOR_NAME)
        tmp = f"{path}.{os.getpid()}.tmp"
        try:
            os.unlink(tmp)
        except OSError:
            pass
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(self.descriptor(), f)
            f.write("\n")
        os.replace(tmp, path)

    def ensure_descriptor(self) -> None:
        if os.path.exists(os.path.join(self.dir, DESCRIPTOR_NAME)):
            return
        try:
            self.write_descriptor()
            self.log("descriptor was missing; rewritten")
        except OSError as exc:
            self.log(f"descriptor rewrite failed: {exc}")

    def log(self, text: str) -> None:
        with self.log_lock:
            if self.log_closed:
                return
            try:
                with open(os.path.join(self.dir, LOG_NAME), "a", encoding="utf-8") as f:
                    f.write(time.strftime("%Y-%m-%dT%H:%M:%S%z") + " " + text + "\n")
            except OSError:
                pass

    # -- connections --

    def _accept_loop(self) -> None:
        while not self.exit_event.is_set():
            try:
                sock, _ = self.listener.accept()
            except OSError:
                return
            threading.Thread(target=self._serve, args=(sock,), daemon=True).start()

    def _serve(self, sock: socket.socket) -> None:
        att = None
        try:
            sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            sock.settimeout(AUTH_TIMEOUT_SECONDS)
            sock.sendall(encode_line({"hello": "ws-holder", "protocol": PROTOCOL_VERSION}))
            reader = sock.makefile("rb")
            att = self._authenticate(sock, reader)
            if att is None:
                return
            sock.settimeout(None)
            while True:
                line = reader.readline(MAX_LINE_BYTES + 1)
                if not line or len(line) > MAX_LINE_BYTES:
                    return
                self._handle_line(att, line)
        except (OSError, ValueError):
            return
        finally:
            if att is None:
                try:
                    sock.close()
                except OSError:
                    pass
            else:
                self._detach(att)

    def _authenticate(self, sock: socket.socket, reader) -> Attachment | None:
        line = reader.readline(65536)
        refusal = None
        req: dict = {}
        try:
            req = json.loads(line)
            if not isinstance(req, dict):
                raise ValueError
        except ValueError:
            req, refusal = {}, ("bad_request", "the first line must be a JSON attach request")
        accepted = {v for v in (PROTOCOL_VERSION, PROTOCOL_VERSION - 1) if v >= 1}
        token = req.get("token")
        spoken = req.get("protocol")
        if refusal is None and req.get("op") != "attach":
            refusal = ("bad_request", "the first operation must be attach")
        if refusal is None and not (
            isinstance(token, str) and hmac.compare_digest(token.encode("utf-8"), self.token.encode("utf-8"))
        ):
            refusal = ("token", "the attach token was refused")
        if refusal is None and (isinstance(spoken, bool) or spoken not in accepted):
            refusal = ("protocol", f"this holder speaks protocol {PROTOCOL_VERSION} and the one before it")
        if refusal is not None:
            sock.sendall(
                encode_line(
                    {
                        "reply": req.get("id"),
                        "ok": False,
                        "error": refusal[0],
                        "message": refusal[1],
                        "protocol": PROTOCOL_VERSION,
                    }
                )
            )
            return None
        att = Attachment(self, sock)
        offered = req.get("audit")
        # Only a holder that skips lines has anything to tee: one without a
        # skip pattern does not take the channel, so the daemon never waits
        # for fences it will not send.
        if self.ring_skip is not None and isinstance(offered, int) and not isinstance(offered, bool) and offered >= AUDIT_VERSION:
            att.audit = AUDIT_VERSION
        with self.cond:
            if self.exit_event.is_set():
                return None
            old, self.attachment = self.attachment, att
            self.ring.set_delivered_before(0)
            if old is not None:
                old.push({"event": "detached", "reason": "replaced"})
                old.finish()
            reply = {
                "reply": req.get("id"),
                "ok": True,
                "protocol": spoken,
                "holder": {
                    "id": self.id,
                    "pid": os.getpid(),
                    "started_at": STARTED_AT,
                    "script_hash": self.script_hash,
                    "restart_safe": self.restart_safe,
                    "session": self.session,
                },
                "child": dict(self.child_status),
                "first_seq": self.ring.first_seq(),
                "next_seq": self.ring.next_seq,
            }
            if att.audit:
                reply["audit"] = att.audit
            att.push(reply)
        att.thread.start()
        return att

    def _detach(self, att: Attachment) -> None:
        att.kill()
        with self.cond:
            if self.attachment is att:
                self.attachment = None
                self.ring.set_delivered_before(0)
                self.idle_since = time.monotonic()

    def _handle_line(self, att: Attachment, line: bytes) -> None:
        try:
            req = json.loads(line)
            if not isinstance(req, dict):
                raise ValueError
        except ValueError:
            req = {"op": None}
        reply = {"reply": req.get("id"), "ok": True}
        # One critical section: the reply is queued before a stop the
        # operation asked for can close the connection.
        with self.cond:
            if self.attachment is not att:
                return
            try:
                handler = self._OPS.get(req.get("op"))
                if handler is None:
                    raise OpError("unknown_op", f"unknown operation {req.get('op')!r}")
                reply.update(handler(self, att, req) or {})
            except OpError as err:
                reply = {"reply": req.get("id"), "ok": False, "error": err.code, "message": err.message}
            att.push(reply)

    # -- operations (called with self.cond held) --

    def _op_spawn(self, att: Attachment, req: dict) -> dict:
        if self.child is not None:
            raise OpError("already_spawned", "this holder already has a child")
        if self.stopping:
            raise OpError("stopping", "the holder is stopping")
        spec = parse_spawn_spec(req)
        try:
            child = spawn_child(spec)
        except OpError:
            raise
        except Exception as exc:  # OSError, a ctypes failure, a bad argument
            raise OpError("spawn_failed", f"{type(exc).__name__}: {exc}") from None
        self.child = child
        capture = spec["http2_capture"]
        if capture is not None and child.mode == "pipe":
            self.http2_capture = Http2Capture(capture["path"], capture["max_bytes"], self.log)
        self.child_status = {"state": "running", "pid": child.pid, "mode": child.mode}
        if child.mode == "pipe":
            self.child_status["input_closed"] = False
        # Queued under the lock before the writer starts: the data and EOF
        # reach the child in order even when it exits at once.
        self._queue_input(spec["input"], spec["input_eof"])
        self.log(f"spawned child pid {child.pid} ({child.mode}): {spec['argv'][0]}")
        readers = []
        for stream, source in child.outputs():
            t = threading.Thread(target=self._read_output, args=(child, stream, source), daemon=True)
            t.start()
            readers.append(t)
        threading.Thread(target=self._write_input, args=(child,), daemon=True).start()
        threading.Thread(target=self._watch_child, args=(child, readers), daemon=True).start()
        return {"child": dict(self.child_status)}

    def _running_child(self):
        if self.child is None or self.child_status["state"] != "running":
            raise OpError("no_child", "no child is running")
        return self.child

    def _op_input(self, att: Attachment, req: dict) -> None:
        self._running_child()
        data = parse_input_data(req.get("data"))
        if self.child_status.get("input_closed"):
            raise OpError("input_closed", "the child's stdin was closed by an earlier eof")
        if self.input_pending + len(data) > MAX_PENDING_INPUT:
            raise OpError("input_overflow", "the child is not reading its input")
        self._queue_input(data, req.get("eof") is True)

    def _queue_input(self, data: bytes, eof: bool) -> None:
        if data:
            self.input_queue.append(data)
            self.input_pending += len(data)
        if eof:
            self.input_queue.append(None)
            if "input_closed" in self.child_status:
                self.child_status["input_closed"] = True
        self.cond.notify_all()

    def _op_resize(self, att: Attachment, req: dict) -> None:
        child = self._running_child()
        rows, cols = parse_size(req)
        try:
            child.resize(rows, cols)
        except OSError as exc:
            raise OpError("resize_failed", str(exc)) from None

    def _op_signal(self, att: Attachment, req: dict) -> None:
        child = self._running_child()
        name = req.get("name")
        if name not in SIGNAL_NAMES:
            raise OpError("bad_request", "signal name must be one of " + ", ".join(SIGNAL_NAMES))
        try:
            child.signal(name)
        except (OSError, ValueError) as exc:
            raise OpError("signal_failed", str(exc)) from None

    def _op_stop(self, att: Attachment, req: dict) -> None:
        self.request_stop()

    def _op_status(self, att: Attachment, req: dict) -> dict:
        return {
            "child": dict(self.child_status),
            "first_seq": self.ring.first_seq(),
            "next_seq": self.ring.next_seq,
        }

    def _op_capture_note(self, att: Attachment, req: dict) -> dict:
        text = req.get("text")
        if not isinstance(text, str) or not text:
            raise OpError("bad_request", "capture_note needs a text string")
        data = text.encode("utf-8", "replace").replace(b"\r", b" ").replace(b"\n", b" ")[:HTTP2_NOTE_MAX]
        capture = self.http2_capture
        return {"captured": capture is not None and capture.write(data)}

    def _op_replay(self, att: Attachment, req: dict) -> dict:
        from_seq = req.get("from", 0)
        if not isinstance(from_seq, int) or isinstance(from_seq, bool):
            raise OpError("bad_request", "replay from must be an integer")
        att.start_stream(from_seq)
        return {"first_seq": self.ring.first_seq(), "next_seq": self.ring.next_seq}

    _OPS = {
        "spawn": _op_spawn,
        "input": _op_input,
        "resize": _op_resize,
        "signal": _op_signal,
        "stop": _op_stop,
        "status": _op_status,
        "replay": _op_replay,
        "capture_note": _op_capture_note,
    }

    # -- the child's threads --

    def _read_output(self, child, stream: str, source) -> None:
        splitter = LineSplitter(self.max_record) if child.mode == "pipe" else None
        skip = self.ring_skip if splitter is not None and stream == "stdout" else None
        router = None
        if splitter is not None and stream == "stderr" and self.http2_capture is not None:
            router = Http2Router(self.http2_capture)
        at_start = True
        while True:
            chunk = child.read(source)
            if not chunk:
                break
            if splitter is None:
                pieces = [(chunk[i : i + self.max_record], False) for i in range(0, len(chunk), self.max_record)]
            else:
                pieces = list(splitter.feed(chunk))
            if router is not None:
                pieces = router.route(pieces)
            if skip is not None:
                marked, at_start = split_skipped(skip, pieces, at_start)
                self._append_marked(stream, marked)
            else:
                self._append_output(stream, pieces)
        if splitter is not None:
            pieces = list(splitter.flush())
            if router is not None:
                pieces = router.route(pieces)
            if skip is not None:
                marked, at_start = split_skipped(skip, pieces, at_start)
                self._append_marked(stream, marked)
                with self.cond:
                    self._audit_fence(0, self.tee_anchor, final=True)
            else:
                self._append_output(stream, pieces)

    def _append_marked(self, stream: str, marked) -> None:
        """Append kept pieces to the ring in order and tee the skipped lines
        to the audit channel, fencing each kept run (see the docstring). The
        pulse, when due, is a kept line after the chunk's pieces."""
        if not marked:
            return
        at = time.time_ns()
        with self.cond:
            run = []
            skipped = False
            for kept, data, partial in marked:
                if kept:
                    run.append((data, partial))
                    continue
                skipped = True
                if run:
                    self._append_run(stream, run)
                    run = []
                self._audit_tee(data, at)
            # The pulse is a line of its own: never inside a cut line.
            if skipped and self.ring_pulse is not None and not marked[-1][2]:
                now = time.monotonic()
                if now - self.ring_pulse_at >= self.ring_pulse_seconds:
                    run.append((self.ring_pulse, False))
                    self.ring_pulse_at = now
            if run:
                self._append_run(stream, run)
            self.cond.notify_all()

    def _append_run(self, stream: str, run) -> None:
        """Append consecutive kept pieces and fence the interval before them
        (called with cond held). The whole lines are fenced at once; a
        trailing cut line's head waits for the line's end: no line ends
        inside it, so no skipped line can follow it before the line's rest,
        and the fence names whole lines only."""
        first = self.tee_run_first
        through = 0
        tail = 0
        for data, partial in run:
            seq = self.ring.append("output", stream, data, partial).seq
            first = first or seq
            if partial:
                tail = tail or seq
            else:
                through, tail = seq, 0
        if through:
            self._audit_fence(first, through)
            first = tail
        self.tee_run_first = first

    def _audit_tee(self, data: bytes, at: int) -> None:
        """Send one skipped line on the audit channel, or count it dropped
        (called with cond held). Never blocks the reader."""
        self.tee_index += 1
        att = self.attachment
        message = {
            "event": "audit",
            "anchor": self.tee_anchor,
            "index": self.tee_index,
            "at": at,
            "data": base64.b64encode(data).decode("ascii"),
        }
        if att is not None and att.push_audit(message, tee=True):
            self.tee_sent += 1
        else:
            self.tee_dropped += 1

    def _audit_fence(self, next_seq: int, through: int, final: bool = False) -> None:
        """Close the open audit interval (called with cond held): the kept
        lines next_seq..through follow it; a final fence closes the last."""
        att = self.attachment
        delivered = False
        if att is not None and att.audit:
            message = {
                "event": "audit_fence",
                "anchor": self.tee_anchor,
                "tee_count_sent": self.tee_sent,
                "dropped_count": self.tee_dropped,
            }
            if self.tee_carry:
                message["lost_before"] = self.tee_carry
            if final:
                message["final"] = True
            else:
                message["next"] = next_seq
                message["through"] = through
            delivered = att.push_audit(message, tee=False)
        if delivered:
            self.tee_carry = 0
        else:
            self.tee_carry += self.tee_dropped
        if not final:
            self.tee_anchor = through
        self.tee_index = self.tee_sent = self.tee_dropped = 0

    def _append_output(self, stream: str, pieces) -> None:
        if not pieces:
            return
        with self.cond:
            for data, partial in pieces:
                self.ring.append("output", stream, data, partial)
            self.cond.notify_all()

    def _write_input(self, child) -> None:
        while True:
            with self.cond:
                while not self.input_queue:
                    if self.child_status["state"] != "running":
                        return
                    self.cond.wait()
                data = self.input_queue.pop(0)
            try:
                if data is None:
                    child.close_input()
                else:
                    child.write(data)
            except (OSError, ValueError):
                pass
            if data is not None:
                with self.cond:
                    self.input_pending -= len(data)

    def _watch_child(self, child, readers) -> None:
        fields = child.wait()
        child.after_exit()
        # The output still in the pipes comes before the exit record; a
        # grandchild holding a pipe open does not delay it past this wait.
        deadline = time.monotonic() + 2.0
        for t in readers:
            t.join(timeout=max(0.0, deadline - time.monotonic()))
        if self.http2_capture is not None:
            self.http2_capture.close()
        with self.cond:
            self.child_status = {"state": "exited", "pid": child.pid, "mode": child.mode, **fields}
            # The totals let a replay whose output records all left the
            # ring still place the job's byte counts.
            totals = {"stream_bytes": dict(self.ring.stream_bytes), "output_bytes": self.ring.output_bytes}
            self.ring.append("exit", fields={**fields, **totals})
            self.idle_since = time.monotonic()
            self.input_queue.clear()
            self.input_pending = 0
            self.cond.notify_all()
        self.log(f"child pid {child.pid} exited: {fields}")

    # -- stop --

    def request_stop(self) -> None:
        """End the child and exit; safe from any thread, and idempotent."""
        with self.cond:
            if self.stopping:
                return
            self.stopping = True
            if self.child_status["state"] == "exited":
                self.exit_reason = "child_exited"
        threading.Thread(target=self._stop, daemon=True).start()

    def _stop(self) -> None:
        self.log("stop requested")
        with self.cond:
            child = self.child if self.child_status["state"] == "running" else None
        if child is not None:
            # An interactive shell ignores TERM; a hangup is what ends it.
            first = "HUP" if child.mode == "pty" and not WINDOWS else "TERM"
            for name, wait in ((first, self.stop_grace), ("KILL", 5.0)):
                try:
                    child.signal(name)
                except (OSError, ValueError, OpError):
                    pass
                with self.cond:
                    if self.cond.wait_for(lambda: self.child_status["state"] != "running", timeout=wait):
                        break
        self.exit_event.set()


def _write_json_file(path: str, record: dict) -> None:
    """Replace path with record through a temporary file beside it."""
    tmp = f"{path}.{os.getpid()}.tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(record, f)
        f.write("\n")
    os.replace(tmp, path)


def _file_sha256(path: str) -> str:
    try:
        with open(path, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()
    except OSError:
        return ""


def lift_qos_default() -> None:
    """On macOS, raise this thread to the DEFAULT QoS class (0x15).

    A holder started under a utility-clamped ancestor would otherwise pass that
    clamp to every child it spawns and run them on efficiency cores. Threads
    and children created after this call inherit the class. Best effort: any
    failure, and every other OS, is a no-op.
    """
    if sys.platform != "darwin":
        return
    try:
        import ctypes

        ctypes.CDLL(None).pthread_set_qos_class_self_np(0x15, 0)
    except Exception:
        pass


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="ws session holder")
    parser.add_argument("--dir", required=True, help="this holder's state directory")
    parser.add_argument("--id", default=None, help="holder id; defaults to the directory name")
    parser.add_argument("--session", default="{}", help="session descriptor, a JSON object")
    parser.add_argument("--idle-exit-seconds", type=float, default=IDLE_EXIT_SECONDS)
    parser.add_argument("--descriptor-check-seconds", type=float, default=DESCRIPTOR_CHECK_SECONDS)
    parser.add_argument("--ring-bytes", type=int, default=RING_BYTES)
    parser.add_argument("--ring-ceiling-bytes", type=int)
    parser.add_argument("--ring-skip", default=None, help="a regular expression; the pipe child's stdout lines it matches stay out of the ring")
    parser.add_argument("--ring-pulse", default=None, help="a content-free line emitted at a bounded rate while ring-skip drops lines")
    parser.add_argument("--stop-grace-seconds", type=float, default=STOP_GRACE_SECONDS)
    parser.add_argument("--exit-copy", default=None, help="a file that also receives the exit record, outside the state directory")
    parser.add_argument("--restart-unsafe", action="store_true", help="record that this holder may end with its starter")
    args = parser.parse_args(argv)
    lift_qos_default()
    try:
        session = json.loads(args.session)
        if not isinstance(session, dict):
            raise ValueError("not an object")
    except ValueError as exc:
        parser.error(f"--session: {exc}")
    holder = Holder(
        args.dir,
        holder_id=args.id,
        session=session,
        idle_exit=args.idle_exit_seconds,
        descriptor_check=args.descriptor_check_seconds,
        ring_bytes=args.ring_bytes,
        ring_ceiling_bytes=args.ring_ceiling_bytes,
        ring_skip=args.ring_skip,
        ring_pulse=args.ring_pulse,
        stop_grace=args.stop_grace_seconds,
        restart_safe=not args.restart_unsafe,
        exit_copy=args.exit_copy,
    )
    for name in ("SIGTERM", "SIGINT", "SIGHUP"):
        sig = getattr(signal, name, None)
        if sig is not None:
            try:
                signal.signal(sig, lambda *_: holder.request_stop())
            except (OSError, ValueError):
                pass
    try:
        code = holder.run()
    except Exception as exc:
        holder.log(f"fatal: {type(exc).__name__}: {exc}")
        holder.exit_reason = "fatal"
        holder.shutdown()
        raise
    if holder.stopping and not WINDOWS and os.getpgrp() == os.getpid():
        # Started detached, the holder leads its own process group, which a
        # pipe child and its descendants share: nothing of it outlives a stop.
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            os.killpg(0, signal.SIGTERM)
            time.sleep(0.2)
            os.killpg(0, signal.SIGKILL)
        except OSError:
            pass
    return code


if __name__ == "__main__":
    sys.exit(main())
