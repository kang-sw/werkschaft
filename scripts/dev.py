#!/usr/bin/env python3
"""Repository-local developer commands. Not shipped with any plugin package.

    python scripts/dev.py rerun daemon [--local] [--no-seed] [--health-window SECONDS]

`rerun daemon` builds agents-plugin-tool/cmd/ws-mcp from the working tree
(cross-built on the sys_lock remote w5090 by default, `go build` here with
--local), stages it under the dashboard daemon's bin/ directory exactly as the launcher
does, and runs `daemon-start --replace --build-time <now>` under the
launcher's starter gate, holding it through bounded REST confirmation of the
expected build. Session launchers reconnect on their own.

The command monitors that build for five minutes by default and restores the
previous retained executable on failure. --health-window 0 skips monitoring
and rollback. Runtime seeding waits for success.

It then seeds the same build into each local-devenv package dir of this checkout
(a valid .local-devenv-runtime marker) with the launcher's own runtime name and
compatibility stamp, so the next session start does not rebuild the same source
locally. --no-seed skips that.

The daemon is started with WS_RSRC_ROOT/WS_SKILLS_ROOT pointing at this
checkout's agents-plugin-wsflow package, as its launcher would set them: the
daemon resolves the Python launcher it hands to Pi workers as
<dir(WS_RSRC_ROOT)>/bin/ws-mcp-launcher.py, and the staged exe has no package
beside it.
"""
import argparse
from contextlib import ExitStack
import importlib.util
import json
import math
import os
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
TOOL_DIR = REPO / "agents-plugin-tool"
LAUNCHER = REPO / "agents-plugin" / "bin" / "ws-mcp-launcher.py"
RUNTIME_CONTRACT = REPO / "agents-plugin" / "runtime.json"

# The machine-wide lock front end, and the named remote worker builds go to.
SYS_LOCK = "D:/sys_lock.sh"
REMOTE = "w5090"
REMOTE_BUILD_MINUTES = 15
# Repo-relative directory the remote build writes into; remote only, never
# fetched wholesale.
REMOTE_DIST = "agents-plugin-tool/dist-x"

# Package dirs of this checkout whose launcher runtime gets seeded.
SEED_PACKAGES = ("agents-plugin", "agents-plugin-wsflow")

# Package dir whose rsrc/ and skills/ the daemon is started against, as the
# launcher of that package would; its bin/ws-mcp-launcher.py is what Pi
# workers run.
DAEMON_PACKAGE = REPO / "agents-plugin-wsflow"

# One lifecycle budget for starter-gate acquisition, daemon-start and REST
# confirmation. Builds and seeding stay outside this deadline and the gate.
START_TIMEOUT_SECONDS = 60.0
HEALTH_WINDOW_SECONDS = 300
HEALTH_INTERVAL_SECONDS = 20
ROLLBACK_STOP_SECONDS = 15


def load_launcher():
    """Import the launcher module for its staging and cache-root helpers.

    Its main() is guarded by __name__, so importing runs no launch. Bytecode
    is not written: that would drop a __pycache__ into the shipped bin/.
    """
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("ws_mcp_launcher", LAUNCHER)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {LAUNCHER}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def binary_name(launcher) -> str:
    return "ws-mcp.exe" if launcher.host_os() == "windows" else "ws-mcp"


def build_local(launcher, scratch: Path) -> Path:
    go = shutil.which("go")
    if not go:
        raise RuntimeError("go is not on PATH")
    out = scratch / binary_name(launcher)
    print(f"building locally: {TOOL_DIR / 'cmd' / 'ws-mcp'}")
    launcher.generate_package_assets(TOOL_DIR)
    identity = launcher.source_runtime_metadata(TOOL_DIR)
    os_name = launcher.host_os()
    build_env = launcher.local_devenv_build_env(os_name)
    proc = subprocess.run(
        [go, "build", "-o", str(out), "./cmd/ws-mcp"],
        cwd=str(TOOL_DIR),
        env=build_env,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"go build exited {proc.returncode}")
    launcher.codesign_darwin_binary(out, os_name, build_env)
    launcher.write_runtime_metadata(out, identity)
    return out


def remote_available() -> str | None:
    """None when the sys_lock remote can build; otherwise why it cannot."""
    if os.name != "nt":
        return "sys_lock remotes are driven from the Windows host only"
    bash = shutil.which("bash")
    if not bash or not Path(SYS_LOCK).is_file():
        return f"{SYS_LOCK} or bash is not present"
    try:
        proc = subprocess.run([bash, SYS_LOCK, "remote", "list"], capture_output=True, text=True, timeout=60, check=False)
    except (OSError, subprocess.SubprocessError) as exc:
        return f"sys_lock remote list failed: {exc}"
    if proc.returncode != 0 or REMOTE not in proc.stdout.split():
        return f"remote {REMOTE} is not listed by sys_lock"
    return None


def build_remote(launcher, scratch: Path) -> Path:
    """Cross-build for this host on the sys_lock remote and fetch the binary back.

    The remote is Linux, so GOOS/GOARCH are this host's. The flags match the
    launcher's own source build (no ldflags): the daemon then reports the
    same version string a launcher-built daemon does, "0.49.0-dev".
    """
    name = binary_name(launcher)
    goos, goarch = launcher.host_os(), launcher.host_arch()
    remote_cmd = (
        "export PATH=$HOME/sdk/go/bin:$PATH; "
        f"cd agents-plugin-tool && python3 scripts/generate-package-assets.py && mkdir -p dist-x && "
        f"CGO_ENABLED=0 GOOS={goos} GOARCH={goarch} go build -o dist-x/{name} ./cmd/ws-mcp && "
        f"python3 scripts/write-runtime-metadata.py dist-x/{name}"
    )
    argv = [
        shutil.which("bash") or "bash", SYS_LOCK, "remote", REMOTE, "run", "dev-rerun-daemon", str(REMOTE_BUILD_MINUTES),
        # --sync wants the repo root as a Windows-style path; a subdirectory
        # fails with "sync source missing".
        "--sync", REPO.as_posix(),
        "--fetch", f"{REMOTE_DIST}/{name}={scratch.as_posix()}",
        "--fetch", f"{REMOTE_DIST}/{name}.metadata.json={scratch.as_posix()}",
        "--", remote_cmd,
    ]
    print(f"building on {REMOTE} for {goos}/{goarch}")
    proc = subprocess.run(argv, check=False)
    if proc.returncode != 0:
        raise RuntimeError(f"remote build on {REMOTE} exited {proc.returncode}")
    out = scratch / name
    if not out.is_file() or launcher.read_runtime_metadata(out) is None:
        raise RuntimeError(f"remote build succeeded but {out} and its valid metadata were not fetched")
    return out


def build(launcher, scratch: Path, local: bool) -> Path:
    if not local:
        reason = remote_available()
        if reason is None:
            return build_remote(launcher, scratch)
        print(f"notice: building locally; {reason}")
    return build_local(launcher, scratch)


def check_built(launcher, built: Path) -> None:
    """Print the build's version and whether the launcher's gate would accept it.

    A failed gate is a warning, not an error: the daemon does not apply it,
    but a launcher would refuse the same binary as a runtime.
    """
    identity = launcher.read_runtime_metadata(built)
    if identity is None:
        raise RuntimeError(f"built binary has no valid hash-bound metadata: {built}")
    print(f"built version: {identity['version']}")
    try:
        contract = launcher.read_runtime_contract(RUNTIME_CONTRACT)
        accepted = launcher.runtime_capabilities_compatible(built, contract, allow_same_minor=True)
    except (Exception, SystemExit) as exc:
        print(f"warning: launcher compatibility gate could not be evaluated: {exc}", file=sys.stderr)
        return
    print("launcher compatibility gate: " + ("accepted" if accepted else "REFUSED (launchers would reject this build as a runtime)"))


def describe(health: dict | None) -> str:
    if health is None:
        return "none running"
    return (
        f"version={health.get('version')} pid={health.get('pid')} "
        f"build_time={health.get('build_time')} content_hash={str(health.get('content_hash', ''))[:12]}"
    )


def current_health(launcher, daemon_dir: Path, timeout: float = 3.0) -> tuple[dict | None, dict | None]:
    endpoint = launcher.read_daemon_endpoint(daemon_dir)
    if endpoint is None:
        return None, None
    try:
        return endpoint, launcher.daemon_health(int(endpoint["port"]), timeout)
    except launcher.DaemonHung as exc:
        raise RuntimeError(f"the running daemon is wedged: {exc}") from exc


def run_daemon_start(launcher, staged: Path, build_time: int, plugin_dir: Path = DAEMON_PACKAGE,
                     *, timeout: float = START_TIMEOUT_SECONDS) -> dict:
    env = dict(os.environ)
    # Set, not defaulted: a shell inside a harness session can inherit a
    # versioned plugin-cache path, and this command means this checkout.
    env["WS_RSRC_ROOT"] = str(plugin_dir / "rsrc")
    env["WS_SKILLS_ROOT"] = str(plugin_dir / "skills")
    # The daemon starts session holders with the interpreter that started it.
    if not env.get("WS_MCP_HOLDER_PYTHON") and sys.executable:
        env["WS_MCP_HOLDER_PYTHON"] = sys.executable
    argv = [str(staged), "daemon-start", "--replace", "--build-time", str(build_time)]
    print("running: " + " ".join(argv))
    proc = subprocess.run(
        argv,
        env=env,
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )
    for line in proc.stderr.splitlines():
        print(f"daemon-start: {line}")
    diagnostic = f"daemon-start exited {proc.returncode}: {proc.stdout.strip()}"
    try:
        report = json.loads(proc.stdout.strip().splitlines()[-1])
        if not isinstance(report, dict) or not isinstance(report.get("outcome"), str) or not report["outcome"]:
            raise ValueError("missing outcome")
    except (ValueError, IndexError) as exc:
        if proc.returncode != 0:
            raise RuntimeError(diagnostic) from exc
        raise RuntimeError(f"daemon-start printed no valid report line: {proc.stdout!r}") from exc
    if proc.returncode != 0:
        if proc.returncode != 1 or report["outcome"] != "unavailable":
            raise RuntimeError(diagnostic)
        # Ensure can time out before its detached child publishes the endpoint.
        # Only this result permits REST reconciliation, never another starter.
        print(f"daemon-start: {diagnostic}")
    else:
        print(f"daemon-start: {proc.stdout.strip()}")
    return report


def seed_package(launcher, plugin_dir: Path, built: Path) -> str:
    """Seed the built exe as plugin_dir's launcher runtime; return the report line."""
    os_name = launcher.host_os()
    if launcher.read_local_devenv_contract(plugin_dir, os_name) is None:
        return "not local-devenv"
    contract_path = plugin_dir / "runtime.json"
    contract = launcher.read_runtime_contract(contract_path)
    platform_name = f"{os_name}-{launcher.host_arch()}"
    runtime_dir = Path(os.environ.get("WS_MCP_RUNTIME_DIR", str(plugin_dir / ".runtime" / platform_name)))
    binary = runtime_dir / launcher.runtime_binary_name(contract, contract_path, os_name)
    identity = launcher.read_runtime_metadata(built)
    if identity is None:
        return "skipped (build metadata missing or hash mismatch)"
    if not launcher.runtime_capabilities_compatible(built, contract, allow_same_minor=True):
        return "skipped (build identity incompatible with package)"
    fingerprint = launcher.local_devenv_source_fingerprint(plugin_dir, os_name)
    runtime_dir.mkdir(parents=True, exist_ok=True)
    if not (binary.is_file() and launcher.sha256_file(binary) == launcher.sha256_file(built)):
        tmp = launcher.unique_runtime_temp_path(runtime_dir, f"{binary.name}.seed")
        try:
            shutil.copyfile(built, tmp)
            # A running launcher's exe is locked on Windows and the launcher has
            # no way around that either (it retries, then keeps the old one), so
            # a locked target is skipped rather than worked around.
            os.replace(tmp, binary)
        except OSError as exc:
            tmp.unlink(missing_ok=True)
            return f"skipped ({binary.name} not replaceable, likely in use: {exc})"
    launcher.write_runtime_metadata(binary, identity)
    launcher.write_compatibility_stamp(binary, contract, contract_path, runtime_dir, fingerprint)
    if not launcher.compatibility_stamp_current(binary, contract, contract_path, runtime_dir, fingerprint):
        return "skipped (compatibility stamp not written)"
    return f"seeded {binary}"


def seed_packages(launcher, built: Path) -> None:
    for name in SEED_PACKAGES:
        try:
            result = seed_package(launcher, REPO / name, built)
        except (OSError, RuntimeError, SystemExit) as exc:
            result = f"skipped ({exc})"
        print(f"seed {name}: {result}")


class HealthConfirmationError(RuntimeError):
    pass


def confirm_daemon(launcher, daemon_dir: Path, digest: str, deadline: float, report: dict,
                   *, build_time: int | None = None) -> tuple[dict, dict]:
    after = None
    while (remaining := deadline - time.monotonic()) > 0:
        endpoint, after = current_health(launcher, daemon_dir, timeout=min(3.0, remaining))
        if time.monotonic() >= deadline:
            break
        if (after is not None and after.get("content_hash") == digest
                and (build_time is None or after.get("build_time") == build_time)):
            if report["outcome"] == "unavailable":
                print("daemon-start recovered: expected build confirmed over REST")
            return endpoint, after
        time.sleep(min(0.2, max(0, deadline - time.monotonic())))
    raise HealthConfirmationError(
        f"expected build was not confirmed over /health within the lifecycle deadline "
        f"(outcome {report['outcome']}; observed {describe(after)})"
    )


def rollback_target(launcher, daemon_dir: Path, before: dict | None) -> dict:
    try:
        record = launcher.read_running_executable(daemon_dir)
    except (OSError, ValueError) as exc:
        raise RuntimeError(f"rollback target missing or invalid: {exc}") from exc
    if before is not None and record["binary_sha256"] != before.get("content_hash"):
        raise RuntimeError("rollback target does not match the running daemon")
    return record


def capture_daemon_process(resources: ExitStack, pid: int):
    """Keep an OS reference so rollback cannot signal a recycled PID."""
    if os.name == "nt":
        import _winapi

        handle = _winapi.OpenProcess(0x00100001, False, pid)  # SYNCHRONIZE | PROCESS_TERMINATE
        resources.callback(_winapi.CloseHandle, handle)

        def stop(timeout):
            if _winapi.WaitForSingleObject(handle, 0) == _winapi.WAIT_OBJECT_0:
                return True
            _winapi.TerminateProcess(handle, 1)
            return _winapi.WaitForSingleObject(handle, int(timeout * 1000)) == _winapi.WAIT_OBJECT_0

        return stop
    if hasattr(os, "pidfd_open") and hasattr(signal, "pidfd_send_signal"):
        fd = os.pidfd_open(pid)
        resources.callback(os.close, fd)

        def stop(timeout):
            try:
                signal.pidfd_send_signal(fd, signal.SIGKILL)
            except ProcessLookupError:
                return True
            return bool(select.select([fd], [], [], timeout)[0])

        return stop
    return None


def capture_reported_process(launcher, daemon_dir: Path, resources: ExitStack, report: dict,
                             before: dict | None, digest: str, build_time: int):
    # Even if /health never answered, the starter and the singleton's own
    # record can identify a published successor from this invocation.
    pid = report.get("pid")
    if type(pid) is not int or pid <= 0 or pid == (before or {}).get("pid"):
        return None
    endpoint = launcher.read_daemon_endpoint(daemon_dir)
    if endpoint is None or endpoint.get("pid") != pid:
        return None
    try:
        record = launcher.read_running_executable(daemon_dir)
        if record["binary_sha256"] == digest and record["build_time"] == build_time:
            return capture_daemon_process(resources, pid)
    except (OSError, ValueError):
        pass  # A dead process may already have released the lock.
    return None


def watch_daemon(launcher, port: int, expected: dict, seconds: float) -> dict:
    deadline = time.monotonic() + seconds
    while True:
        time.sleep(min(HEALTH_INTERVAL_SECONDS, max(0, deadline - time.monotonic())))
        try:
            health = launcher.daemon_health(port, 3.0)
        except launcher.DaemonHung as exc:
            raise RuntimeError("replacement stopped answering /health") from exc
        if health is None or any(health.get(key) != expected.get(key) for key in ("pid", "content_hash", "build_time")):
            raise RuntimeError(f"replacement failed its /health check (observed {describe(health)})")
        if time.monotonic() >= deadline:
            return health


def restore_daemon(launcher, daemon_dir: Path, previous: dict, stop) -> dict:
    deadline = time.monotonic() + ROLLBACK_STOP_SECONDS
    if stop is not None and not stop(ROLLBACK_STOP_SECONDS):
        raise RuntimeError("replacement process did not exit")
    while not launcher.daemon_absence_proven(daemon_dir):
        if stop is None:
            raise RuntimeError("dashboard lock is held and the replacement process could not be identified safely")
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise RuntimeError("dashboard lock did not release after terminating the replacement")
        time.sleep(min(0.2, remaining))
    deadline = time.monotonic() + START_TIMEOUT_SECONDS
    report = run_daemon_start(launcher, Path(previous["executable"]), previous["build_time"])
    _, health = confirm_daemon(launcher, daemon_dir, previous["binary_sha256"],
                               deadline, report)
    return health


def rerun_daemon(local: bool, seed: bool = True, health_window: float = HEALTH_WINDOW_SECONDS) -> int:
    launcher = load_launcher()
    env = dict(os.environ)
    daemon_dir = launcher.proxy_daemon_dir(env)
    bin_dir = daemon_dir / launcher.DAEMON_BIN_DIR_NAME

    with tempfile.TemporaryDirectory(prefix="ws-mcp-dev-") as scratch:
        built = build(launcher, Path(scratch), local)
        check_built(launcher, built)
        digest = launcher.sha256_file(built)
        result = restart_daemon(launcher, daemon_dir, bin_dir, built, digest, health_window)
        if result == 0 and seed:
            seed_packages(launcher, built)
        return result


def restart_daemon(launcher, daemon_dir: Path, bin_dir: Path, built: Path, digest: str, health_window: float) -> int:
    deadline = time.monotonic() + START_TIMEOUT_SECONDS
    starter = launcher.LauncherStartLock(daemon_dir)
    try:
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError("lifecycle deadline expired waiting for the launcher starter lock")
            if starter.acquire():
                break
            time.sleep(min(0.2, remaining))

        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise RuntimeError("lifecycle deadline expired before daemon-start")
        _, before = current_health(launcher, daemon_dir, timeout=min(3.0, remaining))
        print(f"daemon before: {describe(before)}")
        if time.monotonic() >= deadline:
            raise RuntimeError("lifecycle deadline expired before daemon-start")
        previous = None
        if health_window:
            previous = rollback_target(launcher, daemon_dir, before)
            target = bin_dir / launcher.staged_daemon_name(built, digest)
            if target.resolve() == Path(previous["executable"]).resolve() and digest != previous["binary_sha256"]:
                raise RuntimeError("staging would overwrite the rollback target")
        # Staging leaves other content-addressed entries alone. The daemon
        # replaces the last-running record, so retain the old value in memory.
        staged = launcher.stage_daemon_binary(built, bin_dir)
        print(f"staged: {staged}")
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise RuntimeError("lifecycle deadline expired before daemon-start")
        build_time = int(time.time() * 1000)
        with ExitStack() as process:
            stop = None
            report = None
            after = None
            try:
                report = run_daemon_start(launcher, staged, build_time, timeout=remaining)
                endpoint, after = confirm_daemon(launcher, daemon_dir, digest, deadline, report,
                                                 build_time=build_time if health_window else None)
                if health_window:
                    pid = after.get("pid")
                    if (type(pid) is int and pid > 0 and pid == endpoint.get("pid")
                            and pid != (before or {}).get("pid") and after.get("build_time") == build_time):
                        try:
                            stop = capture_daemon_process(process, pid)
                        except OSError:
                            pass  # A dead process may already have released the lock.
                    after = watch_daemon(launcher, int(endpoint["port"]), after, health_window)
                print(f"daemon after:  {describe(after)}")
                return 0
            except (OSError, RuntimeError, subprocess.SubprocessError) as exc:
                if previous is None:
                    if isinstance(exc, HealthConfirmationError):
                        print(f"error: {exc}", file=sys.stderr)
                        return 1
                    raise
                failed = f"{staged.name} ({digest[:12]})"
                if after is None and report is not None:
                    stop = capture_reported_process(launcher, daemon_dir, process, report, before, digest, build_time)
                try:
                    restored = restore_daemon(launcher, daemon_dir, previous, stop)
                except (OSError, RuntimeError, subprocess.SubprocessError) as rollback_error:
                    print(f"error: build {failed} failed ({exc}); rollback to {previous['version']} "
                          f"failed ({rollback_error}); manual action is needed", file=sys.stderr)
                else:
                    print(f"error: build {failed} failed ({exc}); restored {describe(restored)}", file=sys.stderr)
                return 1
    finally:
        starter.close()


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="dev", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    rerun = sub.add_parser("rerun", help="replace a running component with the working-tree build")
    rerun_sub = rerun.add_subparsers(dest="target", required=True)
    daemon = rerun_sub.add_parser("daemon", help="replace the dashboard daemon")
    daemon.add_argument("--local", action="store_true", help=f"build with the local go instead of on the {REMOTE} sys_lock remote")
    daemon.add_argument("--no-seed", action="store_true", help="do not seed the build into the local-devenv plugin runtime dirs")
    daemon.add_argument("--health-window", type=float, default=HEALTH_WINDOW_SECONDS, metavar="SECONDS",
                        help="monitor the replacement before accepting it (default: 300, 0 disables)")
    args = parser.parse_args(argv)
    try:
        if args.command == "rerun" and args.target == "daemon":
            if not math.isfinite(args.health_window) or args.health_window < 0:
                daemon.error("--health-window must be finite and non-negative")
            return rerun_daemon(args.local, not args.no_seed, args.health_window)
    except (RuntimeError, OSError, subprocess.SubprocessError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
