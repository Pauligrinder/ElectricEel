//! Client for the persistent tesla-session companion process (see
//! helper/session/), which keeps one *vehicle.Vehicle BLE session alive
//! across many commands instead of paying a full connect+StartSession
//! handshake per command, the way spawning a fresh `tesla-control` does.
//!
//! Talks newline-delimited JSON over the child's stdin/stdout, matching
//! `Run()`'s own (ok, stdout, stderr, `exit_code`) shape so callers can't tell
//! the difference except by latency. Any failure at this layer (spawn,
//! broken pipe, timeout, a response that doesn't decode or doesn't match
//! the request it's replying to) drops the whole child and reports an
//! error - `Helper::run()` in helper.rs falls back to the one-shot
//! `run_binary("tesla-control", ...)` path on any such error, so a bug
//! here degrades to today's behavior rather than to a broken app.
//!
//! Requests are never sent concurrently: the only caller is `Helper::run`,
//! itself serialized by `ble_sem`, so a single in-flight request at a time
//! is a precondition here, not something this module enforces on its own.

use std::io::{BufRead, BufReader, Write};
use std::path::PathBuf;
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{self, RecvTimeoutError};
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use serde::{Deserialize, Serialize};

/// How long a BLE session may sit idle before tesla-session tears it down
/// and lets the vehicle/adapter go back to sleep. Not user-configurable
/// (see `KNOWN_ISSUES.md`: shipped as an invisible optimization, not a
/// Settings toggle).
pub const IDLE_TIMEOUT_SEC: u32 = 90;

#[derive(Serialize)]
struct Request<'a> {
    id: &'a str,
    cmd: &'a str,
    args: &'a [String],
}

#[derive(Deserialize)]
struct Response {
    id: String,
    ok: bool,
    stdout: String,
    stderr: String,
    exit_code: i32,
}

pub struct RunOutcome {
    pub ok: bool,
    pub stdout: String,
    pub stderr: String,
    pub exit_code: i32,
}

#[derive(Debug)]
pub enum SessionError {
    Spawn(std::io::Error),
    BrokenPipe,
    Timeout,
    Decode(serde_json::Error),
    /// The response's id didn't match the request that was just sent - a
    /// protocol violation (stray/duplicate line, or a bug in
    /// tesla-session), treated as fatal for this child rather than
    /// silently pairing a request with the wrong reply.
    IdMismatch,
}

impl std::fmt::Display for SessionError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            SessionError::Spawn(e) => write!(f, "failed to spawn tesla-session: {e}"),
            SessionError::BrokenPipe => write!(f, "tesla-session pipe closed"),
            SessionError::Timeout => write!(f, "tesla-session did not respond in time"),
            SessionError::Decode(e) => write!(f, "malformed tesla-session response: {e}"),
            SessionError::IdMismatch => write!(f, "tesla-session response id mismatch"),
        }
    }
}

struct ChildHandle {
    child: Child,
    stdin: ChildStdin,
    rx: mpsc::Receiver<String>,
}

/// Spawns a background thread that owns the child's stdout for its whole
/// lifetime, forwarding complete lines onto a channel - this is what makes
/// `recv_timeout` in `SessionClient::run` a real read-with-timeout despite
/// `std::process::ChildStdout` not natively supporting one. EOF or a read
/// error just ends the thread; the channel closing is how `run` finds out.
fn spawn_reader(stdout: std::process::ChildStdout) -> mpsc::Receiver<String> {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let mut reader = BufReader::new(stdout);
        let mut line = String::new();
        loop {
            line.clear();
            match reader.read_line(&mut line) {
                Ok(0) | Err(_) => break,
                Ok(_) => {
                    if tx.send(line.trim_end().to_string()).is_err() {
                        break;
                    }
                }
            }
        }
    });
    rx
}

pub struct SessionClient {
    bin_path: PathBuf,
    child: Mutex<Option<ChildHandle>>,
    next_id: AtomicU64,
}

impl SessionClient {
    pub fn new(bin_path: PathBuf) -> Self {
        SessionClient {
            bin_path,
            child: Mutex::new(None),
            next_id: AtomicU64::new(1),
        }
    }

    /// Kills and forgets any live tesla-session child. Called when
    /// `SetConfig` changes the VIN/key/timeouts a running session was
    /// spawned with - those are only read once, at spawn time (see `run`),
    /// so a stale session must not survive a config change.
    ///
    /// `run()` holds `child`'s lock for the full duration of a BLE op (up
    /// to the connect+command+10s envelope, so potentially minutes) - a
    /// `SetConfig`/`GenerateKey` call invoking this concurrently blocks on
    /// that same lock until the in-flight command finishes, not just until
    /// the child is idle. Harmless today (the feature this guards is off
    /// by default and unverified on real hardware - see `KNOWN_ISSUES.md`),
    /// but worth knowing before relying on `SetConfig` feeling instant
    /// while a command is running.
    pub fn invalidate(&self) {
        if let Ok(mut guard) = self.child.lock() {
            if let Some(mut handle) = guard.take() {
                let _ = handle.child.kill();
                let _ = handle.child.wait();
            }
        }
    }

    fn spawn(
        &self,
        vin: &str,
        key_file: &str,
        connect_timeout_sec: i32,
        command_timeout_sec: i32,
    ) -> Result<ChildHandle, SessionError> {
        let mut child = Command::new(&self.bin_path)
            .arg("-vin")
            .arg(vin)
            .arg("-key-file")
            .arg(key_file)
            .arg("-connect-timeout")
            .arg(format!("{connect_timeout_sec}s"))
            .arg("-command-timeout")
            .arg(format!("{command_timeout_sec}s"))
            .arg("-idle-timeout")
            .arg(format!("{IDLE_TIMEOUT_SEC}s"))
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            // Inherited, not discarded: tesla-session's own startup
            // failures (bad flags, an unexpected panic) should land in
            // teslacontrold's journal, same place run_binary's captured
            // tesla-control stderr effectively ends up via Run()'s reply.
            .stderr(Stdio::inherit())
            .spawn()
            .map_err(SessionError::Spawn)?;

        let stdin = child.stdin.take().expect("piped stdin");
        let stdout = child.stdout.take().expect("piped stdout");
        let rx = spawn_reader(stdout);
        Ok(ChildHandle { child, stdin, rx })
    }

    /// Kills the given handle outright rather than dropping it - Child's
    /// Drop impl does not send a signal, so an abandoned handle would
    /// otherwise leak a running tesla-session (and its BLE session) for
    /// every timeout/error, not just close our end of the pipe.
    fn kill(mut handle: ChildHandle) {
        let _ = handle.child.kill();
        let _ = handle.child.wait();
    }

    #[allow(clippy::too_many_arguments)]
    pub fn run(
        &self,
        cmd: &str,
        args: &[String],
        vin: &str,
        key_file: &str,
        connect_timeout_sec: i32,
        command_timeout_sec: i32,
        timeout: Duration,
    ) -> Result<RunOutcome, SessionError> {
        let mut guard = self.child.lock().unwrap();
        if guard.is_none() {
            *guard = Some(self.spawn(vin, key_file, connect_timeout_sec, command_timeout_sec)?);
        }
        let handle = guard.as_mut().unwrap();

        let id = self.next_id.fetch_add(1, Ordering::Relaxed).to_string();
        let mut line = serde_json::to_string(&Request { id: &id, cmd, args })
            .expect("Request only contains strings; cannot fail to encode");
        line.push('\n');

        if handle.stdin.write_all(line.as_bytes()).is_err() || handle.stdin.flush().is_err() {
            let handle = guard.take().unwrap();
            Self::kill(handle);
            return Err(SessionError::BrokenPipe);
        }

        match handle.rx.recv_timeout(timeout) {
            Ok(raw) => {
                let resp: Response = match serde_json::from_str(&raw) {
                    Ok(r) => r,
                    Err(e) => {
                        let handle = guard.take().unwrap();
                        Self::kill(handle);
                        return Err(SessionError::Decode(e));
                    }
                };
                if resp.id != id {
                    let handle = guard.take().unwrap();
                    Self::kill(handle);
                    return Err(SessionError::IdMismatch);
                }
                Ok(RunOutcome {
                    ok: resp.ok,
                    stdout: resp.stdout,
                    stderr: resp.stderr,
                    exit_code: resp.exit_code,
                })
            }
            Err(RecvTimeoutError::Timeout | RecvTimeoutError::Disconnected) => {
                let handle = guard.take().unwrap();
                Self::kill(handle);
                Err(SessionError::Timeout)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // No real tesla-session binary (and no BLE adapter) in this
    // environment - what's verifiable here is that a missing/non-
    // executable binary is a clean Spawn error, not a panic or hang, and
    // that invalidate() on a client that never spawned anything is a
    // harmless no-op. The write/read/timeout/id-matching logic above is
    // exercised for real by helper/session's own tests on the child
    // process side; the two halves only meet on a real device.
    #[test]
    fn test_spawn_failure_is_reported_not_panicked() {
        let client = SessionClient::new(PathBuf::from("/nonexistent/tesla-session"));
        let result = client.run(
            "lock",
            &[],
            "5YJ3E1EA0PF000000",
            "/nonexistent/key.pem",
            5,
            5,
            Duration::from_secs(1),
        );
        assert!(matches!(result, Err(SessionError::Spawn(_))));
    }

    #[test]
    fn test_invalidate_without_a_running_child() {
        let client = SessionClient::new(PathBuf::from("/nonexistent/tesla-session"));
        client.invalidate();
        client.invalidate();
    }
}
