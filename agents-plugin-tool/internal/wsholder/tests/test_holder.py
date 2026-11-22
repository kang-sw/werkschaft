"""Behavior tests for the session holder script (holder.py).

Each test runs a Holder in-process on a thread with shortened periods and
talks to it over a real loopback socket, using small Python test children so
the tests run on POSIX and on Windows alike.
"""

import base64
import datetime
import importlib.util
import json
import os
import shutil
import socket
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

HOLDER_PATH = Path(__file__).resolve().parent.parent / "holder.py"
_spec = importlib.util.spec_from_file_location("ws_session_holder", HOLDER_PATH)
holder_mod = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(holder_mod)

WINDOWS = os.name == "nt"
PY = sys.executable
TIMEOUT = 10.0


def py_child(code: str) -> list:
    return [PY, "-u", "-c", code]


# A child that runs until it is signalled.
LONG_SLEEPER = py_child("import time\nprint('ready', flush=True)\ntime.sleep(60)\n")
# A child that runs until its input is closed.
STDIN_EXIT = py_child("import sys\nprint('ready', flush=True)\nsys.stdin.read()\n")


def wait_until(pred, timeout=TIMEOUT, interval=0.02) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if pred():
            return True
        time.sleep(interval)
    return pred()


def retry_sharing(action):
    """Run a file action, retrying a short while on PermissionError: on
    Windows another process (a file scanner) may briefly hold a file the
    holder has just written."""
    deadline = time.monotonic() + 5.0
    while True:
        try:
            return action()
        except PermissionError:
            if not WINDOWS or time.monotonic() >= deadline:
                raise
            time.sleep(0.05)


def read_json(path: str):
    def read():
        with open(path, encoding="utf-8") as f:
            return json.load(f)

    return retry_sharing(read)


def decode(msg: dict) -> bytes:
    return base64.b64decode(msg.get("data", ""))


def is_output(msg, needle: bytes, stream=None) -> bool:
    return (
        msg is not None
        and msg.get("event") == "output"
        and (stream is None or msg.get("stream") == stream)
        and needle in decode(msg)
    )


class Client:
    """A line-JSON client for one attach connection."""

    def __init__(self, port: int) -> None:
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=TIMEOUT)
        self.reader = self.sock.makefile("rb")
        self.pending = []
        self.ids = 0
        self.hello = self._read()

    def _read(self):
        line = self.reader.readline()
        if not line:
            return None
        return json.loads(line)

    def send(self, message: dict) -> None:
        self.sock.sendall(json.dumps(message).encode("utf-8") + b"\n")

    def _next_id(self) -> int:
        self.ids += 1
        return self.ids

    def attach(self, token, protocol=None) -> dict:
        rid = self._next_id()
        msg = {"op": "attach", "id": rid, "token": token}
        msg["protocol"] = holder_mod.PROTOCOL_VERSION if protocol is None else protocol
        self.send(msg)
        return self.wait_reply(rid)

    def request(self, op: str, fields=None) -> dict:
        rid = self._next_id()
        self.send({"op": op, "id": rid, **(fields or {})})
        return self.wait_reply(rid)

    def wait_reply(self, rid) -> dict:
        while True:
            msg = self._read()
            if msg is None:
                raise EOFError("connection closed before the reply")
            if msg.get("reply") == rid:
                return msg
            self.pending.append(msg)

    def next_event(self):
        if self.pending:
            return self.pending.pop(0)
        return self._read()

    def events_until(self, pred) -> list:
        """Messages up to and including the first one pred accepts."""
        out = []
        while True:
            msg = self.next_event()
            if msg is None:
                raise EOFError(f"connection closed; got {out!r}")
            out.append(msg)
            if pred(msg):
                return out

    def at_eof(self) -> bool:
        return not self.pending and self._read() is None

    def close(self) -> None:
        for closer in (self.reader.close, lambda: self.sock.shutdown(socket.SHUT_RDWR), self.sock.close):
            try:
                closer()
            except OSError:
                pass


class HolderTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.mkdtemp(prefix="ws-holder-test-")
        self.addCleanup(shutil.rmtree, self.tmp, ignore_errors=True)

    def start_holder(self, name="h1", **kwargs):
        kwargs.setdefault("idle_exit", 60.0)
        kwargs.setdefault("descriptor_check", 60.0)
        kwargs.setdefault("stop_grace", 1.0)
        directory = os.path.join(self.tmp, name)
        holder = holder_mod.Holder(directory, holder_id=name, session={"kind": "test"}, **kwargs)
        holder.result = {}
        holder.thread = threading.Thread(target=lambda: holder.result.setdefault("code", holder.run()), daemon=True)
        holder.thread.start()
        self.addCleanup(self._end_holder, holder)
        self.assertTrue(wait_until(lambda: holder.port != 0 and os.path.exists(self.descriptor_path(holder))))
        return holder

    def _end_holder(self, holder) -> None:
        holder.request_stop()
        holder.thread.join(timeout=15.0)
        child = holder.child
        if child is not None:
            proc = getattr(child, "proc", None)
            if proc is not None and proc.poll() is None:
                proc.kill()
                proc.wait(timeout=5.0)
            process = getattr(child, "process", None)
            if process is not None and child.k32.WaitForSingleObject(process, 0) != 0:
                child.k32.TerminateProcess(process, 1)
        self.assertFalse(holder.thread.is_alive(), "holder did not end")

    def descriptor_path(self, holder) -> str:
        return os.path.join(holder.dir, holder_mod.DESCRIPTOR_NAME)

    def connect(self, holder) -> Client:
        client = Client(holder.port)
        self.addCleanup(client.close)
        return client

    def attached(self, holder, stream=True) -> Client:
        client = self.connect(holder)
        reply = client.attach(holder.token)
        self.assertTrue(reply["ok"], reply)
        if stream:
            self.assertTrue(client.request("replay", {"from": 0})["ok"])
        return client

    def spawn(self, client, argv, **fields) -> dict:
        reply = client.request("spawn", {"argv": argv, **fields})
        self.assertTrue(reply["ok"], reply)
        return reply

    def wait_exit_record(self, client) -> dict:
        return client.events_until(lambda m: m.get("event") == "exit")[-1]


class ProtocolVersionTest(HolderTestCase):
    def test_banner_attach_reply_and_descriptor_report_the_version(self):
        holder = self.start_holder()
        client = self.connect(holder)
        self.assertEqual(client.hello, {"hello": "ws-holder", "protocol": holder_mod.PROTOCOL_VERSION})
        reply = client.attach(holder.token)
        self.assertTrue(reply["ok"], reply)
        self.assertEqual(reply["protocol"], holder_mod.PROTOCOL_VERSION)
        self.assertEqual(reply["holder"]["id"], "h1")
        self.assertEqual(reply["holder"]["session"], {"kind": "test"})
        self.assertEqual(reply["child"], {"state": "none"})
        self.assertEqual((reply["first_seq"], reply["next_seq"]), (1, 1))
        desc = read_json(self.descriptor_path(holder))
        self.assertEqual(desc["protocol"], holder_mod.PROTOCOL_VERSION)
        self.assertEqual(desc["token"], holder.token)
        self.assertEqual(desc["port"], holder.port)
        self.assertEqual(desc["pid"], os.getpid())

    def test_accepts_its_version_and_the_one_before_and_refuses_others(self):
        # Only one protocol version exists today, so raise the holder's
        # version to make "the one before it" a real accepted version.
        with mock.patch.object(holder_mod, "PROTOCOL_VERSION", 2):
            holder = self.start_holder()
            for spoken in (2, 1):
                client = self.connect(holder)
                self.assertEqual(client.hello["protocol"], 2)
                reply = client.attach(holder.token, protocol=spoken)
                self.assertTrue(reply["ok"], reply)
                self.assertEqual(reply["protocol"], spoken)
            for spoken in (0, 3, "2", True):
                client = self.connect(holder)
                reply = client.attach(holder.token, protocol=spoken)
                self.assertFalse(reply["ok"], reply)
                self.assertEqual(reply["error"], "protocol")
                self.assertEqual(reply["protocol"], 2)
                self.assertTrue(client.at_eof())

    def test_version_one_has_no_version_before_it(self):
        with mock.patch.object(holder_mod, "PROTOCOL_VERSION", 1):
            holder = self.start_holder()
            self.assertTrue(self.connect(holder).attach(holder.token, protocol=1)["ok"])
            for spoken in (0, 2):
                reply = self.connect(holder).attach(holder.token, protocol=spoken)
                self.assertEqual(reply["error"], "protocol", reply)


class AuthenticationTest(HolderTestCase):
    def test_attach_without_or_with_a_wrong_token_is_refused_and_closed(self):
        holder = self.start_holder()
        for token in (None, "", "0" * 64, holder.token[:-1]):
            client = self.connect(holder)
            rid = 7
            msg = {"op": "attach", "id": rid, "protocol": holder_mod.PROTOCOL_VERSION}
            if token is not None:
                msg["token"] = token
            client.send(msg)
            reply = client.wait_reply(rid)
            self.assertEqual(reply["ok"], False)
            self.assertEqual(reply["error"], "token")
            self.assertTrue(client.at_eof())
        # A refused attach does not displace the holder's state: a right
        # token still works.
        self.assertTrue(self.connect(holder).attach(holder.token)["ok"])

    def test_first_line_must_be_an_attach(self):
        holder = self.start_holder()
        client = self.connect(holder)
        client.send({"op": "status", "id": 1, "token": holder.token})
        reply = client.wait_reply(1)
        self.assertEqual(reply["error"], "bad_request")
        self.assertTrue(client.at_eof())

    @unittest.skipIf(WINDOWS, "POSIX file modes do not apply on Windows")
    def test_descriptor_is_private_to_the_owner(self):
        holder = self.start_holder()
        self.assertEqual(os.stat(self.descriptor_path(holder)).st_mode & 0o777, 0o600)
        self.assertEqual(os.stat(holder.dir).st_mode & 0o777, 0o700)


class RingTest(unittest.TestCase):
    def test_undelivered_records_stay_until_sent(self):
        cost = holder_mod.Record(0, "output", data=b"x").cost()
        ring = holder_mod.Ring(2 * cost, 4 * cost)
        for _ in range(4):
            ring.append("output", "stdout", b"x")
        self.assertEqual(ring.size, 4 * cost)
        self.assertEqual(ring.first_seq(), 1)

        ring.set_delivered_before(2)
        self.assertEqual(ring.size, 3 * cost)
        self.assertEqual(ring.first_seq(), 2)
        self.assertIsNone(ring.items[0])

        ring.set_delivered_before(4)
        self.assertEqual(ring.size, 2 * cost)
        self.assertEqual([r.seq for r in ring.items], [3, 4])
        self.assertEqual(ring.head, 0)

        # New output can replace already sent history without growing the ring.
        ring.append("output", "stdout", b"x")
        self.assertEqual(ring.size, 2 * cost)
        self.assertEqual(ring.first_seq(), 4)

    def test_ceiling_evicts_oldest_even_without_delivery(self):
        cost = holder_mod.Record(0, "output", data=b"x").cost()
        ring = holder_mod.Ring(cost, 3 * cost)
        for _ in range(5):
            ring.append("output", "stdout", b"x")
        self.assertEqual(ring.size, 3 * cost)
        self.assertEqual([r.seq for r in ring.batch(3, 10, 10 * cost)], [3, 4, 5])

    def test_replay_reprotects_previously_sent_records(self):
        cost = holder_mod.Record(0, "output", data=b"x").cost()
        ring = holder_mod.Ring(cost, 2 * cost)
        ring.append("output", "stdout", b"x")
        ring.set_delivered_before(2)
        ring.set_delivered_before(1)
        ring.append("output", "stdout", b"x")
        self.assertEqual(ring.first_seq(), 1)
        self.assertEqual(ring.size, 2 * cost)

    def test_output_offsets_count_each_stream_and_all_output(self):
        ring = holder_mod.Ring(1 << 20)
        a = ring.append("output", "stdout", b"abc")
        b = ring.append("output", "stderr", b"de")
        c = ring.append("output", "stdout", b"f")
        exit_rec = ring.append("exit", fields={"exit_code": 0, "signal": None})
        self.assertEqual([(r.offset, r.combined_offset) for r in (a, b, c)], [(0, 0), (0, 3), (3, 5)])
        self.assertEqual(c.message()["offset"], 3)
        self.assertEqual(c.message()["combined_offset"], 5)
        self.assertNotIn("offset", exit_rec.message())

    def test_newest_stays_even_when_it_exceeds_the_budget(self):
        ring = holder_mod.Ring(64, 128)
        ring.append("output", "stdout", b"x")
        newest = ring.append("output", "stdout", b"x" * 128)
        ring.set_delivered_before(ring.next_seq)
        self.assertEqual(ring.first_seq(), newest.seq)
        self.assertEqual(ring.batch(newest.seq, 10, 1024), [newest])


class RingStreamTest(unittest.TestCase):
    def test_large_pipe_line_reaches_a_slow_attachment_without_gaps(self):
        record_bytes = 1 << 20
        count = 100
        base = 32 << 20
        holder = holder_mod.Holder(".", ring_bytes=base, ring_ceiling_bytes=1 << 30)
        writer, reader = socket.socketpair()
        self.addCleanup(reader.close)
        self.addCleanup(writer.close)
        writer.setsockopt(socket.SOL_SOCKET, socket.SO_SNDBUF, 32 << 10)
        reader.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 32 << 10)
        reader.settimeout(TIMEOUT)
        att = holder_mod.Attachment(holder, writer)
        with holder.cond:
            holder.attachment = att
            att.start_stream(1)
        att.thread.start()
        self.addCleanup(att.thread.join, TIMEOUT)
        self.addCleanup(att.kill)

        # The first socket send is in flight when the rest of the burst arrives.
        # The reader holds off until the child could have emitted the whole line.
        holder._append_output("stdout", [(b"x" * record_bytes, True)])
        self.assertTrue(wait_until(lambda: att.cursor == 2))
        for _ in range(count - 1):
            holder._append_output("stdout", [(b"x" * record_bytes, True)])
        holder._append_output("stdout", [(b"\n", False)])
        with holder.cond:
            buffered_size = holder.ring.size
            self.assertEqual(holder.ring.delivered_before, 0)

        with reader.makefile("rb") as stream:
            for seq in range(1, count + 2):
                msg = json.loads(stream.readline())
                self.assertEqual(msg["event"], "output", msg.get("event"))
                self.assertEqual(msg["seq"], seq)
                self.assertEqual(msg.get("partial", False), seq <= count)
                self.assertEqual(len(decode(msg)), record_bytes if seq <= count else 1)

        self.assertGreater(buffered_size, base)
        self.assertTrue(wait_until(lambda: holder.ring.delivered_before == count + 2))
        with holder.cond:
            self.assertLessEqual(holder.ring.size, base)
            self.assertEqual(holder.ring.head, 0)
            self.assertEqual(len(holder.ring.items), holder.ring.next_seq - holder.ring.first_seq())


class RingReplayTest(HolderTestCase):
    def _filled_holder(self):
        # 200 lines of 10 bytes cost far more than the 2000-byte ring holds.
        holder = self.start_holder(ring_bytes=2000)
        client = self.attached(holder, stream=False)
        self.spawn(client, py_child("import sys\nfor i in range(200): sys.stdout.buffer.write(b'line %04d\\n' % i)\n"))
        self.assertTrue(
            wait_until(lambda: client.request("status")["child"]["state"] == "exited"),
        )
        status = client.request("status")
        self.assertGreater(status["first_seq"], 1)
        self.assertEqual(status["next_seq"], 202)  # 200 lines and the exit record
        client.close()
        return holder, status["first_seq"]

    def _replay(self, holder, from_seq):
        client = self.attached(holder, stream=False)
        reply = client.request("replay", {"from": from_seq})
        self.assertTrue(reply["ok"], reply)
        events = client.events_until(lambda m: m.get("event") == "exit")
        client.close()
        return reply, events

    def test_replay_reports_a_gap_when_the_sequence_left_the_ring(self):
        holder, first = self._filled_holder()
        reply, events = self._replay(holder, 1)
        self.assertEqual(reply["first_seq"], first)
        self.assertEqual(events[0], {"event": "gap", "from": 1, "to": first})
        seqs = [m["seq"] for m in events[1:]]
        self.assertEqual(seqs, list(range(first, 202)))
        self.assertEqual(decode(events[-2]), b"line 0199\n")
        self.assertEqual(events[-1]["exit_code"], 0)

    def test_replay_from_zero_or_below_sends_the_whole_ring_without_a_gap(self):
        holder, first = self._filled_holder()
        for from_seq in (0, -5):
            _, events = self._replay(holder, from_seq)
            self.assertEqual([m["seq"] for m in events], list(range(first, 202)))

    def test_replayed_records_carry_their_absolute_byte_offsets(self):
        holder, first = self._filled_holder()
        _, events = self._replay(holder, 0)
        # Each line is 10 bytes on stdout, the only stream.
        for msg in events[:-1]:
            self.assertEqual(msg["offset"], (msg["seq"] - 1) * 10)
            self.assertEqual(msg["combined_offset"], msg["offset"])
        self.assertGreater(events[0]["offset"], 0)
        self.assertNotIn("offset", events[-1])

    def test_exit_record_carries_the_output_totals(self):
        holder, first = self._filled_holder()
        # Only the exit record is replayed: its totals still give the counts.
        _, events = self._replay(holder, 201)
        self.assertEqual([m["event"] for m in events], ["exit"])
        self.assertEqual(events[0]["stream_bytes"], {"stdout": 2000})
        self.assertEqual(events[0]["output_bytes"], 2000)

    def test_replay_from_a_held_sequence_starts_there(self):
        holder, first = self._filled_holder()
        _, events = self._replay(holder, first + 5)
        self.assertEqual([m["seq"] for m in events], list(range(first + 5, 202)))
        self.assertEqual(decode(events[0]), b"line %04d\n" % (first + 5 - 1))


class RingSkipTest(HolderTestCase):
    def test_skipped_stdout_lines_take_no_sequence_and_reach_no_client(self):
        holder = self.start_holder(ring_skip="^drop")
        client = self.attached(holder)
        code = (
            "import sys\n"
            "sys.stdout.buffer.write(b'keep 1\\ndrop 2\\nkeep drop 3\\ndrop 4\\n')\n"
            "sys.stderr.buffer.write(b'drop on stderr\\n')\n"
        )
        self.spawn(client, py_child(code), mode="pipe")
        events = client.events_until(lambda m: m.get("event") == "exit")
        stdout = [decode(m) for m in events if m.get("stream") == "stdout"]
        stderr = [decode(m) for m in events if m.get("stream") == "stderr"]
        self.assertEqual(stdout, [b"keep 1\n", b"keep drop 3\n"])
        self.assertEqual(stderr, [b"drop on stderr\n"])
        self.assertEqual(sorted(m["seq"] for m in events), list(range(1, 5)))

    def test_only_whole_lines_are_skipped(self):
        pattern = holder_mod.re.compile(b"^drop")
        pieces = [(b"keep", True), (b"drop tail\n", False), (b"drop\n", False), (b"drop cut", True), (b"rest\n", False)]
        kept, at_start, skipped = holder_mod.unskipped(pattern, pieces, True)
        self.assertEqual(kept, [(b"keep", True), (b"drop tail\n", False), (b"drop cut", True), (b"rest\n", False)])
        self.assertTrue(at_start)
        self.assertTrue(skipped)
        kept, at_start, skipped = holder_mod.unskipped(pattern, [(b"drop\n", False)], False)
        self.assertEqual(kept, [(b"drop\n", False)])
        self.assertTrue(at_start)
        self.assertFalse(skipped)

    def test_skipped_lines_emit_throttled_content_free_pulse(self):
        pulse = '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"ws_activity"}}}'
        holder = self.start_holder(ring_skip="^secret", ring_pulse=pulse, ring_pulse_seconds=60.0)
        client = self.attached(holder)
        self.spawn(client, py_child("print('secret thought one')\nprint('secret thought two')\n"), mode="pipe")
        events = client.events_until(lambda m: m.get("event") == "exit")
        output = [decode(m) for m in events if m.get("stream") == "stdout"]
        self.assertEqual(output, [pulse.encode("utf-8") + b"\n"])
        self.assertNotIn(b"thought", b"".join(output))


class AuditChannelTest(HolderTestCase):
    """The negotiated audit channel: skipped stdout lines tee'd with an
    anchor, fences at kept-run boundaries, and nothing for an old client."""

    def attach_audit(self, holder, offer=1, stream=True):
        client = self.connect(holder)
        rid = client._next_id()
        msg = {"op": "attach", "id": rid, "token": holder.token, "protocol": holder_mod.PROTOCOL_VERSION}
        if offer is not None:
            msg["audit"] = offer
        client.send(msg)
        reply = client.wait_reply(rid)
        self.assertTrue(reply["ok"], reply)
        if stream:
            self.assertTrue(client.request("replay", {"from": 0})["ok"])
        return client, reply

    CODE = (
        "import sys\n"
        "sys.stdout.buffer.write(b'keep 1\\ndrop a\\ndrop b\\nkeep 2\\nkeep 3\\ndrop c\\n')\n"
        "sys.stdout.flush()\n"
    )

    def test_a_client_that_offers_no_audit_gets_none(self):
        holder = self.start_holder(ring_skip="^drop")
        client, reply = self.attach_audit(holder, offer=None)
        self.assertNotIn("audit", reply)
        self.spawn(client, py_child(self.CODE), mode="pipe")
        events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertFalse([m for m in events if str(m.get("event", "")).startswith("audit")])
        self.assertEqual([decode(m) for m in events if m.get("stream") == "stdout"], [b"keep 1\n", b"keep 2\n", b"keep 3\n"])

    def test_skipped_lines_are_teed_with_anchors_and_fenced_in_order(self):
        holder = self.start_holder(ring_skip="^drop")
        client, reply = self.attach_audit(holder)
        self.assertEqual(reply["audit"], holder_mod.AUDIT_VERSION)
        self.spawn(client, py_child(self.CODE), mode="pipe")
        events = client.events_until(lambda m: m.get("event") == "exit")
        seq_of = {decode(m): m["seq"] for m in events if m.get("stream") == "stdout"}
        k1, k2, k3 = seq_of[b"keep 1\n"], seq_of[b"keep 2\n"], seq_of[b"keep 3\n"]
        tees = [m for m in events if m.get("event") == "audit"]
        self.assertEqual([decode(m) for m in tees], [b"drop a\n", b"drop b\n", b"drop c\n"])
        self.assertEqual([(m["anchor"], m["index"]) for m in tees], [(k1, 1), (k1, 2), (k3, 1)])
        for m in tees:
            self.assertIsInstance(m["at"], int)
            self.assertNotIn("seq", m)
        fences = [m for m in events if m.get("event") == "audit_fence"]
        # The interval before keep 1 (no skips), after keep 1 (two sent),
        # and the final one after keep 3 (one sent).
        self.assertEqual(
            [(f["anchor"], f.get("next"), f.get("through"), f["tee_count_sent"], f["dropped_count"], f.get("final", False)) for f in fences],
            [(0, k1, k1, 0, 0, False), (k1, k2, k3, 2, 0, False), (k3, None, None, 1, 0, True)],
        )
        order = [m for m in events if m.get("event") in ("audit", "audit_fence") or m.get("stream") == "stdout"]
        pos = {id(m): i for i, m in enumerate(order)}
        # Audit messages go ahead of ring records: in one chunk, a tee line
        # reaches the client before the kept line it is anchored on. The
        # daemon's capture orders them by anchor, not by arrival.
        k1_msg = next(m for m in order if m.get("seq") == k1)
        self.assertLess(pos[id(tees[0])], pos[id(k1_msg)])
        # Each interval's tee lines precede its fence, and a fence precedes
        # the kept lines it releases.
        self.assertLess(pos[id(tees[1])], pos[id(fences[1])])
        k2_msg = next(m for m in order if m.get("seq") == k2)
        self.assertLess(pos[id(fences[1])], pos[id(k2_msg)])
        self.assertLess(pos[id(tees[2])], pos[id(fences[2])])

    def test_lines_skipped_while_no_daemon_is_attached_are_counted_dropped(self):
        holder = self.start_holder(ring_skip="^drop")
        client = self.attached(holder)
        code = (
            "import sys, time\n"
            "print('keep 1', flush=True)\n"
            "sys.stdin.readline()\n"
            "print('drop x', flush=True)\n"
            "print('drop y', flush=True)\n"
            "sys.stdin.readline()\n"
            "print('keep 2', flush=True)\n"
        )
        self.spawn(client, py_child(code), mode="pipe")
        client.events_until(lambda m: is_output(m, b"keep 1"))
        client.close()
        self.assertTrue(wait_until(lambda: holder.attachment is None))
        holder.cond.acquire()
        try:
            holder._queue_input(b"go\n", False)
        finally:
            holder.cond.release()
        self.assertTrue(wait_until(lambda: holder.tee_dropped == 2))
        client2, _ = self.attach_audit(holder)
        self.assertTrue(client2.request("input", {"data": base64.b64encode(b"go\n").decode()})["ok"])
        events = client2.events_until(lambda m: m.get("event") == "exit")
        self.assertFalse([m for m in events if m.get("event") == "audit"])
        fences = [m for m in events if m.get("event") == "audit_fence" and not m.get("final")]
        self.assertEqual([(f["tee_count_sent"], f["dropped_count"]) for f in fences], [(0, 2)])

    def test_a_holder_without_a_skip_pattern_does_not_take_the_channel(self):
        holder = self.start_holder()
        client, reply = self.attach_audit(holder)
        self.assertNotIn("audit", reply)
        self.spawn(client, py_child("print('one')\nprint('two')\n"), mode="pipe")
        events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertFalse([m for m in events if str(m.get("event", "")).startswith("audit")])

    def test_drops_of_intervals_fenced_while_detached_reach_the_next_fence(self):
        holder = self.start_holder(ring_skip="^drop")
        client = self.attached(holder)
        code = (
            "import sys\n"
            "print('keep 1', flush=True)\n"
            "sys.stdin.readline()\n"
            "print('drop x', flush=True)\n"
            "print('keep 2', flush=True)\n"
            "print('drop y', flush=True)\n"
            "print('drop z', flush=True)\n"
            "print('keep 3', flush=True)\n"
            "sys.stdin.readline()\n"
            "print('keep 4', flush=True)\n"
        )
        self.spawn(client, py_child(code), mode="pipe")
        client.events_until(lambda m: is_output(m, b"keep 1"))
        client.close()
        self.assertTrue(wait_until(lambda: holder.attachment is None))
        with holder.cond:
            holder._queue_input(b"go\n", False)
        self.assertTrue(wait_until(lambda: holder.tee_carry == 3))
        client2, _ = self.attach_audit(holder)
        self.assertTrue(client2.request("input", {"data": base64.b64encode(b"go\n").decode()})["ok"])
        events = client2.events_until(lambda m: m.get("event") == "exit")
        fences = [m for m in events if m.get("event") == "audit_fence" and not m.get("final")]
        self.assertEqual([(f["tee_count_sent"], f["dropped_count"], f.get("lost_before", 0)) for f in fences], [(0, 0, 3)])

    def test_a_read_ending_in_a_cut_line_head_fences_its_whole_lines(self):
        # One read holds a whole kept line and the head of a line longer
        # than one record (a 4 KiB ring cuts at 4 KiB, under the 64 KiB
        # read). The whole line's fence goes out at once; only the cut
        # line's run waits for the line's end, which the child holds back
        # until its input arrives.
        holder = self.start_holder(ring_skip="^drop", ring_bytes=4096, ring_ceiling_bytes=1 << 20)
        client, _ = self.attach_audit(holder)
        code = (
            "import sys\n"
            "sys.stdout.buffer.write(b'keep 1\\n' + b'x' * 10000)\n"
            "sys.stdout.flush()\n"
            "sys.stdin.readline()\n"
            "sys.stdout.buffer.write(b'y' * 100 + b'\\ndrop a\\nkeep 2\\n')\n"
            "sys.stdout.flush()\n"
        )
        self.spawn(client, py_child(code), mode="pipe")
        early = client.events_until(lambda m: m.get("event") == "audit_fence" and m.get("through"))
        seq_of = {decode(m): m["seq"] for m in early if m.get("stream") == "stdout"}
        fence = early[-1]
        k1 = fence["through"]
        self.assertEqual((fence["anchor"], fence["next"], fence["tee_count_sent"]), (0, k1, 0))
        self.assertTrue(client.request("input", {"data": base64.b64encode(b"go\n").decode()})["ok"])
        events = early + client.events_until(lambda m: m.get("event") == "exit")
        records = [m for m in events if m.get("stream") == "stdout"]
        seq_of = {decode(m): m["seq"] for m in records}
        self.assertEqual(seq_of[b"keep 1\n"], k1)
        cut = [m["seq"] for m in records if m.get("partial") or decode(m).startswith(b"x") or decode(m).startswith(b"y")]
        k2 = seq_of[b"keep 2\n"]
        fences = [m for m in events if m.get("event") == "audit_fence"]
        # keep 1 alone, then the cut line (its head is no line's anchor),
        # then keep 2 after the teed drop, then the final interval.
        self.assertEqual(
            [(f["anchor"], f.get("next"), f.get("through"), f["tee_count_sent"], f.get("final", False)) for f in fences],
            [(0, k1, k1, 0, False), (k1, cut[0], cut[-1], 0, False), (cut[-1], k2, k2, 1, False), (k2, None, None, 0, True)],
        )

    def test_a_full_audit_queue_drops_tee_lines_and_counts_them(self):
        holder = self.start_holder(ring_skip="^drop")
        client, _ = self.attach_audit(holder)
        with mock.patch.object(holder_mod, "AUDIT_QUEUE_BYTES", 0):
            self.spawn(client, py_child(self.CODE), mode="pipe")
            events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertFalse([m for m in events if m.get("event") == "audit"])
        fences = [m for m in events if m.get("event") == "audit_fence"]
        self.assertEqual([(f["tee_count_sent"], f["dropped_count"]) for f in fences], [(0, 0), (0, 2), (0, 1)])
        self.assertEqual(len([m for m in events if m.get("stream") == "stdout"]), 3)


class IdleExitTest(HolderTestCase):
    def test_never_idles_out_while_the_child_lives_then_exits_after_it(self):
        holder = self.start_holder(idle_exit=0.4)
        client = self.attached(holder)
        self.spawn(client, STDIN_EXIT)
        client.events_until(lambda m: is_output(m, b"ready"))
        client.close()
        # Nobody attached and the child alive: well past the idle period the
        # holder is still up.
        time.sleep(2.0)
        self.assertTrue(holder.thread.is_alive())
        client = self.attached(holder)
        self.assertTrue(client.request("input", {"data": "", "eof": True})["ok"])
        self.wait_exit_record(client)
        # Attached, with the child gone: still up.
        time.sleep(1.5)
        self.assertTrue(holder.thread.is_alive())
        client.close()
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        self.assertEqual(holder.result.get("code"), 0)
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual((record["exit_code"], record["reason"]), (0, "idle"))
        datetime.datetime.fromisoformat(record["stopped_at"].replace("Z", "+00:00"))

    def test_idles_out_when_no_child_was_ever_spawned(self):
        holder = self.start_holder(idle_exit=0.3)
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual((record["exit_code"], record["reason"]), (None, "idle"))

    def test_an_attachment_keeps_a_childless_holder_up(self):
        holder = self.start_holder(idle_exit=0.3)
        client = self.attached(holder)
        time.sleep(1.5)
        self.assertTrue(holder.thread.is_alive())
        self.assertTrue(client.request("status")["ok"])


class TakeoverTest(HolderTestCase):
    def test_newer_authenticated_attach_closes_the_previous_one(self):
        holder = self.start_holder()
        old = self.attached(holder, stream=False)
        new = self.connect(holder)
        self.assertTrue(new.attach(holder.token)["ok"])
        self.assertEqual(old.next_event(), {"event": "detached", "reason": "replaced"})
        self.assertTrue(old.at_eof())
        self.assertTrue(new.request("status")["ok"])
        self.spawn(new, STDIN_EXIT)

    def test_refused_attach_leaves_the_current_one_in_place(self):
        holder = self.start_holder()
        current = self.attached(holder, stream=False)
        self.assertFalse(self.connect(holder).attach("bad")["ok"])
        self.assertTrue(current.request("status")["ok"])


class ChildTest(HolderTestCase):
    def test_pipe_child_streams_echoes_input_and_reports_its_exit_code(self):
        holder = self.start_holder()
        client = self.attached(holder)
        code = (
            "import sys\n"
            "sys.stderr.buffer.write(b'err line\\n'); sys.stderr.flush()\n"
            "for line in sys.stdin.buffer:\n"
            "    sys.stdout.buffer.write(b'got:' + line); sys.stdout.flush()\n"
            "sys.exit(3)\n"
        )
        reply = self.spawn(client, py_child(code), mode="pipe")
        self.assertEqual(reply["child"]["state"], "running")
        self.assertEqual(reply["child"]["mode"], "pipe")
        data = base64.b64encode(b"hello\nworld\n").decode("ascii")
        self.assertTrue(client.request("input", {"data": data, "eof": True})["ok"])
        events = client.events_until(lambda m: m.get("event") == "exit")
        stdout = [decode(m) for m in events if m.get("stream") == "stdout"]
        stderr = [decode(m) for m in events if m.get("stream") == "stderr"]
        self.assertEqual(stdout, [b"got:hello\n", b"got:world\n"])
        self.assertEqual(stderr, [b"err line\n"])
        seqs = [m["seq"] for m in events]
        self.assertEqual(seqs, sorted(seqs))
        self.assertEqual((events[-1]["exit_code"], events[-1]["signal"]), (3, None))
        status = client.request("status")["child"]
        self.assertEqual(status["state"], "exited")
        self.assertEqual(status["exit_code"], 3)
        # The child is gone, so child operations are refused.
        self.assertEqual(client.request("input", {"data": data})["error"], "no_child")

    def test_second_spawn_is_refused(self):
        holder = self.start_holder()
        client = self.attached(holder)
        self.spawn(client, STDIN_EXIT)
        reply = client.request("spawn", {"argv": STDIN_EXIT})
        self.assertEqual(reply["error"], "already_spawned")

    def test_bad_requests_are_refused_without_ending_the_connection(self):
        holder = self.start_holder()
        client = self.attached(holder)
        self.assertEqual(client.request("spawn", {"argv": []})["error"], "bad_request")
        self.assertEqual(client.request("frobnicate")["error"], "unknown_op")
        self.assertEqual(client.request("input", {"data": "aGk="})["error"], "no_child")
        self.spawn(client, STDIN_EXIT)
        self.assertEqual(client.request("resize", {"rows": 10, "cols": 10})["error"], "not_pty")
        self.assertEqual(client.request("signal", {"name": "USR1"})["error"], "bad_request")
        self.assertEqual(client.request("input", {"data": "!!"})["error"], "bad_request")

    def test_signal_by_name_ends_the_child(self):
        holder = self.start_holder()
        client = self.attached(holder)
        self.spawn(client, LONG_SLEEPER)
        client.events_until(lambda m: is_output(m, b"ready"))
        self.assertTrue(client.request("signal", {"name": "TERM"})["ok"])
        exit_rec = self.wait_exit_record(client)
        status = client.request("status")["child"]
        if WINDOWS:
            # Windows has no signals: TERM ends the child's process tree.
            self.assertIsNone(exit_rec["signal"])
            self.assertNotEqual(exit_rec["exit_code"], 0)
        else:
            self.assertEqual((exit_rec["exit_code"], exit_rec["signal"]), (None, 15))
            self.assertEqual(status["signal"], 15)
        self.assertEqual(status["state"], "exited")
        self.assertTrue(client.request("stop")["ok"])
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual(record["reason"], "child_exited")
        self.assertEqual(record["exit_code"], exit_rec["exit_code"])
        self.assertEqual(record["signal"], exit_rec["signal"])

    def test_stop_ends_the_child_and_the_holder(self):
        holder = self.start_holder(stop_grace=1.0)
        client = self.attached(holder)
        self.spawn(client, LONG_SLEEPER)
        client.events_until(lambda m: is_output(m, b"ready"))
        reply = client.request("stop")
        self.assertTrue(reply["ok"], reply)
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        self.assertIsNotNone(holder.child.proc.poll())
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual(record["reason"], "stop")
        self.assertEqual(record["exit_code"], holder.child_status.get("exit_code"))
        self.assertEqual(record["signal"], holder.child_status.get("signal"))
        # The connection drains the remaining records, then closes.
        events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertEqual(events[-1]["event"], "exit")
        self.assertTrue(client.at_eof())

    def test_pty_child_sees_its_size_and_a_resize(self):
        holder = self.start_holder()
        client = self.attached(holder)
        code = (
            "import os,sys\n"
            "def size():\n"
            "    s = os.get_terminal_size(1)\n"
            "    return 'SZ=%dx%d' % (s.lines, s.columns)\n"
            "print(size(), flush=True)\n"
            "sys.stdin.readline()\n"
            "print(size(), flush=True)\n"
            "sys.exit(5)\n"
        )
        reply = self.spawn(client, py_child(code), mode="pty", rows=24, cols=80)
        self.assertEqual(reply["child"]["mode"], "pty")
        seen = bytearray()

        def saw(needle):
            def pred(msg):
                if msg.get("event") == "output":
                    self.assertEqual(msg["stream"], "pty")
                    seen.extend(decode(msg))
                return needle in seen or msg.get("event") == "exit"

            return pred

        client.events_until(saw(b"SZ=24x80"))
        self.assertIn(b"SZ=24x80", seen)
        self.assertTrue(client.request("resize", {"rows": 40, "cols": 100})["ok"])
        self.assertEqual(client.request("resize", {"rows": 0, "cols": 100})["error"], "bad_request")
        data = base64.b64encode(b"go\r").decode("ascii")
        self.assertTrue(client.request("input", {"data": data})["ok"])
        client.events_until(saw(b"SZ=40x100"))
        self.assertIn(b"SZ=40x100", seen)
        exit_rec = self.wait_exit_record(client)
        self.assertEqual(exit_rec["exit_code"], 5)


class SpawnInputTest(HolderTestCase):
    """Spawn's input and input_eof fields: finite stdin queued with the start."""

    ECHO_TO_EOF = py_child(
        "import sys\n"
        "data = sys.stdin.buffer.read()\n"
        "sys.stdout.buffer.write(b'got:' + data + b'|eof\\n'); sys.stdout.flush()\n"
    )

    def stdout_of(self, events) -> bytes:
        return b"".join(decode(m) for m in events if m.get("stream") == "stdout")

    def test_input_and_eof_reach_a_child_that_reads_to_eof(self):
        holder = self.start_holder()
        client = self.attached(holder)
        data = base64.b64encode(b"hello\nworld").decode("ascii")
        reply = self.spawn(client, self.ECHO_TO_EOF, input=data, input_eof=True)
        self.assertEqual(reply["child"]["state"], "running")
        events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertEqual(self.stdout_of(events), b"got:hello\nworld|eof\n")
        self.assertEqual((events[-1]["exit_code"], events[-1]["signal"]), (0, None))

    def test_eof_without_input_is_an_immediate_eof(self):
        holder = self.start_holder()
        client = self.attached(holder)
        self.spawn(client, self.ECHO_TO_EOF, input_eof=True)
        events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertEqual(self.stdout_of(events), b"got:|eof\n")
        self.assertEqual(events[-1]["exit_code"], 0)

    def test_status_reports_stdin_closed_and_input_after_eof_is_refused(self):
        sleeper = py_child("import time; time.sleep(30)")
        holder = self.start_holder()
        client = self.attached(holder, stream=False)
        reply = self.spawn(client, sleeper)
        self.assertIs(reply["child"]["input_closed"], False)
        data = base64.b64encode(b"x").decode("ascii")
        self.assertTrue(client.request("input", {"data": data, "eof": True})["ok"])
        client.close()
        # A rejoining client reads the closed stdin from the attach reply.
        again = self.connect(holder)
        attach = again.attach(holder.token)
        self.assertIs(attach["child"]["input_closed"], True)
        refused = again.request("input", {"data": data})
        self.assertEqual((refused["ok"], refused.get("error")), (False, "input_closed"))

    def test_spawn_with_input_eof_reports_stdin_closed(self):
        holder = self.start_holder()
        client = self.attached(holder, stream=False)
        reply = self.spawn(client, py_child("import time; time.sleep(30)"), input_eof=True)
        self.assertIs(reply["child"]["input_closed"], True)

    def test_input_without_eof_leaves_stdin_open_for_input_operations(self):
        holder = self.start_holder()
        client = self.attached(holder)
        self.spawn(client, self.ECHO_TO_EOF, input=base64.b64encode(b"first,").decode("ascii"))
        self.assertEqual(client.request("status")["child"]["state"], "running")
        more = base64.b64encode(b"second").decode("ascii")
        self.assertTrue(client.request("input", {"data": more, "eof": True})["ok"])
        events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertEqual(self.stdout_of(events), b"got:first,second|eof\n")

    def test_a_child_that_exits_at_once_does_not_fail_the_spawn(self):
        holder = self.start_holder()
        client = self.attached(holder)
        data = base64.b64encode(b"x" * 200000).decode("ascii")
        reply = self.spawn(client, py_child("import sys; sys.exit(2)"), input=data, input_eof=True)
        self.assertEqual(reply["child"]["state"], "running")
        exit_rec = self.wait_exit_record(client)
        self.assertEqual(exit_rec["exit_code"], 2)
        self.assertTrue(client.request("stop")["ok"])
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual((record["exit_code"], record["reason"]), (2, "child_exited"))

    def test_bad_input_refuses_the_spawn_before_the_child_starts(self):
        holder = self.start_holder()
        client = self.attached(holder)
        for bad in ("!!", 5, ["aGk="]):
            reply = client.request("spawn", {"argv": self.ECHO_TO_EOF, "input": bad, "input_eof": True})
            self.assertEqual(reply["error"], "bad_request", reply)
        self.assertIsNone(holder.child)
        self.assertEqual(client.request("status")["child"], {"state": "none"})
        with mock.patch.object(holder_mod, "MAX_PENDING_INPUT", 4):
            reply = client.request("spawn", {"argv": self.ECHO_TO_EOF, "input": base64.b64encode(b"12345").decode("ascii")})
            self.assertEqual(reply["error"], "input_overflow", reply)
        self.assertIsNone(holder.child)
        # The holder can still start its one child.
        self.spawn(client, self.ECHO_TO_EOF, input=base64.b64encode(b"ok").decode("ascii"), input_eof=True)
        events = client.events_until(lambda m: m.get("event") == "exit")
        self.assertEqual(self.stdout_of(events), b"got:ok|eof\n")

    @unittest.skipIf(WINDOWS, "the POSIX PTY end-of-file key")
    def test_pty_input_eof_sends_the_end_of_file_key(self):
        holder = self.start_holder()
        client = self.attached(holder)
        code = "import sys\ndata = sys.stdin.read()\nprint('READ=%r' % data, flush=True)\n"
        data = base64.b64encode(b"hi\n").decode("ascii")
        self.spawn(client, py_child(code), mode="pty", input=data, input_eof=True)
        seen = bytearray()

        def pred(msg):
            if msg.get("event") == "output":
                seen.extend(decode(msg))
            return msg.get("event") == "exit"

        exit_rec = client.events_until(pred)[-1]
        self.assertIn(b"READ='hi\\n'", seen)
        self.assertEqual(exit_rec["exit_code"], 0)


class ExitCopyTest(HolderTestCase):
    def test_exit_copy_matches_exit_json_and_survives_the_state_directory(self):
        copy_path = os.path.join(self.tmp, "elsewhere", "exits", "h1.json")
        holder = self.start_holder(exit_copy=copy_path)
        client = self.attached(holder)
        self.spawn(client, py_child("import sys; sys.exit(9)"))
        self.wait_exit_record(client)
        self.assertFalse(os.path.exists(copy_path))
        self.assertTrue(client.request("stop")["ok"])
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual((record["exit_code"], record["reason"]), (9, "child_exited"))
        self.assertEqual(read_json(copy_path), record)
        if not WINDOWS:
            self.assertEqual(os.stat(copy_path).st_mode & 0o777, 0o600)
            self.assertEqual(os.stat(os.path.dirname(copy_path)).st_mode & 0o777, 0o700)
        retry_sharing(lambda: shutil.rmtree(holder.dir))
        self.assertEqual(read_json(copy_path), record)
        self.assertEqual(os.listdir(os.path.dirname(copy_path)), ["h1.json"])

    def test_a_failed_copy_is_logged_and_keeps_exit_json_and_the_exit(self):
        blocker = os.path.join(self.tmp, "not-a-directory")
        with open(blocker, "w", encoding="utf-8") as f:
            f.write("x")
        holder = self.start_holder(exit_copy=os.path.join(blocker, "h1.json"))
        client = self.attached(holder)
        self.assertTrue(client.request("stop")["ok"])
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        self.assertEqual(holder.result.get("code"), 0)
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual(record["reason"], "stop")
        with open(os.path.join(holder.dir, holder_mod.LOG_NAME), encoding="utf-8") as f:
            self.assertIn("exit record copy failed", f.read())

    def test_main_accepts_the_exit_copy_argument(self):
        captured = {}

        class FakeHolder:
            def __init__(self, directory, **kwargs):
                captured.update(kwargs)
                self.stopping = False

            def request_stop(self):
                pass

            def run(self):
                return 0

        with mock.patch.object(holder_mod, "Holder", FakeHolder), mock.patch.object(holder_mod.signal, "signal"):
            code = holder_mod.main(["--dir", os.path.join(self.tmp, "hx"), "--exit-copy", os.path.join(self.tmp, "c.json")])
            self.assertEqual(code, 0)
            self.assertEqual(captured["exit_copy"], os.path.join(self.tmp, "c.json"))
            holder_mod.main(["--dir", os.path.join(self.tmp, "hy")])
            self.assertIsNone(captured["exit_copy"])


class DescriptorTest(HolderTestCase):
    def test_deleted_descriptor_is_rewritten(self):
        holder = self.start_holder(descriptor_check=0.1)
        path = self.descriptor_path(holder)
        before = read_json(path)
        retry_sharing(lambda: os.unlink(path))
        self.assertTrue(wait_until(lambda: os.path.exists(path)))
        after = read_json(path)
        self.assertEqual(after, before)
        if not WINDOWS:
            self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)

    def test_descriptor_records_the_boot_clock_start_where_the_os_has_one(self):
        holder = self.start_holder()
        desc = read_json(self.descriptor_path(holder))
        if not sys.platform.startswith("linux"):
            self.assertNotIn("start_ticks", desc)
            return
        with open(f"/proc/{os.getpid()}/stat", encoding="ascii") as f:
            stat = f.read()
        # Field 22 of the stat line, counted past the parenthesized name.
        self.assertEqual(desc["start_ticks"], int(stat[stat.rindex(")") + 1 :].split()[19]))

    def test_exit_record_keeps_nonzero_child_code(self):
        holder = self.start_holder()
        client = self.attached(holder)
        self.spawn(client, py_child("import sys; sys.exit(7)"))
        self.wait_exit_record(client)
        self.assertTrue(client.request("stop")["ok"])
        holder.thread.join(timeout=TIMEOUT)
        self.assertFalse(holder.thread.is_alive())
        record = read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME))
        self.assertEqual((record["exit_code"], record["signal"], record["reason"]), (7, None, "child_exited"))
        timestamp = datetime.datetime.fromisoformat(record["stopped_at"].replace("Z", "+00:00"))
        self.assertIsNotNone(timestamp.utcoffset())
        retry_sharing(lambda: os.unlink(self.descriptor_path(holder)))
        self.assertEqual(read_json(os.path.join(holder.dir, holder_mod.EXIT_NAME)), record)
        if not WINDOWS:
            self.assertEqual(os.stat(os.path.join(holder.dir, holder_mod.EXIT_NAME)).st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
