// End-to-end checks of the full stop against real daemons, each started by
// the test in its own temporary cache root and home with an OS-chosen port,
// so no shared daemon or holder is touched. They run only when
// WERKSCHAFT_E2E_WS_MCP names a ws-mcp binary that has the full stop; with
// WERKSCHAFT_E2E_OLD_WS_MCP naming one without it, the older daemon's
// answer is checked too:
//
//   WERKSCHAFT_E2E_WS_MCP=/tmp/ws-mcp-new WERKSCHAFT_E2E_OLD_WS_MCP=/tmp/ws-mcp-old \
//     cargo test --locked full_stop_e2e -- --ignored --test-threads=1

use std::{
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::atomic::{AtomicUsize, Ordering},
    thread,
    time::{Duration, Instant},
};

use serde_json::{json, Value};

use crate::endpoint::{discover_at, Daemon, FullStop};

struct Isolated {
    root: PathBuf,
    children: Vec<Child>,
    holders: Vec<u32>,
}

impl Isolated {
    fn new() -> Self {
        static NEXT: AtomicUsize = AtomicUsize::new(0);
        let root = std::env::temp_dir().join(format!(
            "werkschaft-e2e-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        std::fs::create_dir_all(root.join("home")).unwrap();
        std::fs::create_dir_all(root.join("work")).unwrap();
        Self {
            root,
            children: Vec::new(),
            holders: Vec::new(),
        }
    }

    fn cache(&self) -> PathBuf {
        self.root.join("cache")
    }

    // Starts a daemon in this root and waits until it answers.
    fn start(&mut self, binary: &Path) -> Daemon {
        let previous = discover_at(&self.cache()).ok();
        let child = Command::new(binary)
            .args(["dashboard", "--port", "0", "--lock-wait", "10s"])
            .env_clear()
            .env("PATH", std::env::var_os("PATH").unwrap_or_default())
            .env("HOME", self.root.join("home"))
            .env("TMPDIR", std::env::temp_dir())
            .env("SHELL", "/bin/sh")
            .env("WS_CACHE_HOME", self.cache())
            .env("WS_CONFIG_HOME", self.root.join("config"))
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .expect("start the daemon");
        self.children.push(child);
        let deadline = Instant::now() + Duration::from_secs(30);
        loop {
            if let Ok(daemon) = discover_at(&self.cache()) {
                if previous.as_ref() != Some(&daemon) {
                    return daemon;
                }
            }
            assert!(Instant::now() < deadline, "the daemon did not start");
            thread::sleep(Duration::from_millis(100));
        }
    }

    // Opens a terminal (a shell under a session holder) from the daemon's
    // local anchor, as the dashboard page does, and returns its holder PID.
    fn open_terminal(&mut self, daemon: &Daemon) -> u32 {
        let cookie = daemon
            .browser_cookie()
            .unwrap()
            .expect("a browser credential");
        let origin = daemon.url.origin().ascii_serialization();
        let agent: ureq::Agent = ureq::Agent::config_builder()
            .proxy(None)
            .http_status_as_error(false)
            .timeout_global(Some(Duration::from_secs(30)))
            .build()
            .into();
        let mut answer = None;
        let deadline = Instant::now() + Duration::from_secs(20);
        while answer.is_none() {
            let mut response = agent
                .post(daemon.url.join("/s/host0/terminals").unwrap().as_str())
                .header("Cookie", &format!("{}={}", cookie.name, cookie.value))
                .header("Origin", &origin)
                .send_json(json!({"folder": "path", "path": self.root.join("work"), "rows": 24, "cols": 80}))
                .unwrap();
            match response.status().as_u16() {
                201 => answer = Some(response.body_mut().read_json::<Value>().unwrap()),
                // The daemon is still finding its terminals.
                503 if Instant::now() < deadline => thread::sleep(Duration::from_millis(200)),
                status => panic!(
                    "open a terminal: {status} {}",
                    response.body_mut().read_to_string().unwrap_or_default()
                ),
            }
        }
        let id = answer.unwrap()["id"].as_str().unwrap().to_owned();
        let descriptor: Value = serde_json::from_slice(
            &std::fs::read(
                self.cache()
                    .join("dashboard/holders")
                    .join(&id)
                    .join("descriptor.json"),
            )
            .expect("the holder's descriptor"),
        )
        .unwrap();
        let pid = descriptor["pid"].as_u64().unwrap() as u32;
        self.holders.push(pid);
        pid
    }
}

impl Drop for Isolated {
    fn drop(&mut self) {
        for pid in &self.holders {
            if alive(*pid) {
                // The holder leads its own process group; its shell is in it.
                for target in [format!("-{pid}"), pid.to_string()] {
                    let _ = Command::new("kill")
                        .args(["-9", &target])
                        .stderr(Stdio::null())
                        .status();
                }
            }
        }
        for child in &mut self.children {
            let _ = child.kill();
            let _ = child.wait();
        }
        let _ = std::fs::remove_dir_all(&self.root);
    }
}

fn alive(pid: u32) -> bool {
    Command::new("kill")
        .args(["-0", &pid.to_string()])
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|status| status.success())
}

fn wait_gone(pid: u32) -> bool {
    let deadline = Instant::now() + Duration::from_secs(10);
    while alive(pid) {
        if Instant::now() >= deadline {
            return false;
        }
        thread::sleep(Duration::from_millis(50));
    }
    true
}

fn binary(name: &str) -> Option<PathBuf> {
    std::env::var_os(name)
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
}

#[test]
#[ignore = "starts real daemons; set WERKSCHAFT_E2E_WS_MCP"]
fn full_stop_ends_the_sessions_and_the_daemon_and_reports_a_successor() {
    let Some(new) = binary("WERKSCHAFT_E2E_WS_MCP") else {
        eprintln!("WERKSCHAFT_E2E_WS_MCP is unset; skipped");
        return;
    };
    let mut env = Isolated::new();
    let daemon = env.start(&new);
    assert_eq!(daemon.running_sessions().unwrap(), 0);
    let first = env.open_terminal(&daemon);
    let second = env.open_terminal(&daemon);
    assert_eq!(daemon.running_sessions().unwrap(), 2);

    assert_eq!(daemon.full_stop().unwrap(), FullStop::Stopped);
    assert!(
        wait_gone(first) && wait_gone(second),
        "a terminal holder outlived the full stop"
    );
    assert!(
        daemon.wait_stopped(Duration::from_secs(20)),
        "the daemon did not stop"
    );
    assert!(daemon.successor().is_none());

    // A client outside the stopped sessions starts a fresh daemon: the
    // full quit reports it rather than hiding it.
    let fresh = env.start(&new);
    let successor = daemon.successor().expect("the successor is observed");
    assert!(successor == fresh);
    assert_eq!(fresh.running_sessions().unwrap(), 0);
    assert_eq!(fresh.full_stop().unwrap(), FullStop::Stopped);
    assert!(fresh.wait_stopped(Duration::from_secs(20)));
}

#[test]
#[ignore = "starts real daemons; set WERKSCHAFT_E2E_OLD_WS_MCP"]
fn an_older_daemon_is_unsupported_and_stops_nothing() {
    let Some(old) = binary("WERKSCHAFT_E2E_OLD_WS_MCP") else {
        eprintln!("WERKSCHAFT_E2E_OLD_WS_MCP is unset; skipped");
        return;
    };
    let mut env = Isolated::new();
    let daemon = env.start(&old);
    let holder = env.open_terminal(&daemon);
    assert_eq!(daemon.running_sessions().unwrap(), 1);

    assert_eq!(daemon.full_stop().unwrap(), FullStop::Unsupported);
    thread::sleep(Duration::from_millis(300));
    assert!(
        alive(holder),
        "the older daemon's terminal holder was stopped"
    );
    assert!(daemon.is_current(), "the older daemon stopped");
    assert_eq!(daemon.running_sessions().unwrap(), 1);
}
