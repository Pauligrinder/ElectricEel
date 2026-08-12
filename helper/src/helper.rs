use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use wait_timeout::ChildExt;
use zbus::message::Header;

use crate::authorize::authorize;
use crate::commands::{is_known_command, is_pin_command};
use crate::config::Config;
use crate::error::HelperError;
use crate::session_client::SessionClient;

pub const IFACE_NAME: &str = "org.teslacontrol.Helper1";

/// `GetConfig`'s return payload, in the same order the client (Qt's
/// `QDBusPendingReply`) and the zbus macro both destructure it:
/// (vin, model, `key_name`, `connect_timeout_sec`, `command_timeout_sec`, `has_key`,
/// `public_key_pem`). A transparent alias so the seven-field tuple - above
/// clippy's type-complexity comfort zone, and easy to mismatch by position
/// at the five call sites - is spelled once.
type GetConfigReply = (String, String, String, i32, i32, bool, String);

/// Combined exit status of a bundled binary invocation.
struct RunOutcome {
    ok: bool,
    stdout: String,
    stderr: String,
    exit_code: i32,
}

pub struct Helper {
    cfg: Mutex<Config>,
    /// Capacity-1 semaphore serializing Run/Pair through the single HCI
    /// adapter: a second simultaneous BLE command is rejected rather than
    /// spawning a second tesla-control that fights over the adapter.
    ble_sem: Mutex<()>,
    bin_dir: String,
    state_dir: String,
    allowed_callers: Vec<String>,
    /// A connection dedicated to outbound proxy calls (`GetConnectionCredentials`
    /// in `authorize()`), deliberately separate from the connection the
    /// `ObjectServer` uses to dispatch these very method calls. zbus's own
    /// docs warn against blocking-API calls from within an interface method
    /// on the *same* connection that's driving that method's dispatch (the
    /// "async sandwich" footgun) - a second, independent connection/socket
    /// avoids that reentrancy hazard entirely.
    credentials_conn: zbus::blocking::Connection,
    /// `Some()` only when `TESLACONTROLD_PERSISTENT_SESSION` is set (see
    /// main.rs) - None means `Run()` behaves exactly as before this feature
    /// existed, not "session client that always fails over". Not yet
    /// verified on real hardware; see `KNOWN_ISSUES.md` before enabling it.
    session: Option<SessionClient>,
}

impl Helper {
    pub fn new(
        bin_dir: String,
        state_dir: String,
        allowed_callers: Vec<String>,
        credentials_conn: zbus::blocking::Connection,
        session: Option<SessionClient>,
    ) -> Helper {
        let config_path = Path::new(&state_dir).join("config.json");
        let cfg = match Config::load(&config_path) {
            Ok(cfg) => cfg,
            Err(e) => {
                eprintln!("teslacontrold: cannot read config {}: {e}", config_path.display());
                std::process::exit(1);
            }
        };
        Helper {
            cfg: Mutex::new(cfg),
            ble_sem: Mutex::new(()),
            bin_dir,
            state_dir,
            allowed_callers,
            credentials_conn,
            session,
        }
    }

    fn config_path(&self) -> PathBuf {
        Path::new(&self.state_dir).join("config.json")
    }

    fn private_key_path(&self) -> PathBuf {
        Path::new(&self.state_dir).join("private_key.pem")
    }

    fn public_key_path(&self) -> PathBuf {
        Path::new(&self.state_dir).join("public_key.pem")
    }

    fn authorize_sender(&self, header: &Header<'_>) -> Result<(), HelperError> {
        let sender = header
            .sender()
            .ok_or_else(|| HelperError::Forbidden("cannot resolve caller credentials".to_string()))?
            .to_string();
        authorize(&self.credentials_conn, &sender, &self.allowed_callers)
    }

    /// Builds the -ble/-vin/-key-file/... flags shared by every tesla-control
    /// invocation, from the persisted config.
    fn common_args_locked(&self, cfg: &Config) -> Result<Vec<String>, HelperError> {
        if cfg.vin.is_empty() {
            return Err(HelperError::NotConfigured(
                "VIN is not set; call SetConfig first".to_string(),
            ));
        }
        if std::fs::metadata(self.private_key_path()).is_err() {
            return Err(HelperError::NoKey(
                "no private key; call GenerateKey first".to_string(),
            ));
        }
        Ok(vec![
            "-ble".to_string(),
            "-keyring-type".to_string(),
            "file".to_string(),
            "-key-file".to_string(),
            self.private_key_path().to_string_lossy().into_owned(),
            "-key-name".to_string(),
            cfg.key_name.clone(),
            "-vin".to_string(),
            cfg.vin.clone(),
            "-connect-timeout".to_string(),
            format!("{}s", cfg.connect_timeout_sec),
            "-command-timeout".to_string(),
            format!("{}s", cfg.command_timeout_sec),
        ])
    }
}

// clippy::needless_pass_by_value is a false positive for every method
// below: #[zbus(header)] header is constructed and passed in by value by
// the zbus::interface macro itself (verified empirically - `&Header<'_>`
// is a type mismatch against the macro-generated call site, not a style
// choice), and D-Bus "in" parameters like run()'s `args: Vec<String>`
// can't become `&[String]` because zvariant has no owner to borrow a
// `Vec<String>` from while deserializing the message body (unlike a bare
// `&str`, which zvariant *can* deserialize zero-copy - see set_config's
// vin/key_name, which clippy correctly flagged and are fixed below).
#[allow(clippy::needless_pass_by_value)]
#[zbus::interface(name = "org.teslacontrol.Helper1")]
impl Helper {
    /// Executes a single tesla-control subcommand. cmd must be one of the
    /// known tesla-control subcommands; args are passed through verbatim as
    /// positional arguments (never as additional flags).
    fn run(
        &self,
        cmd: String,
        args: Vec<String>,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<(bool, String, String, i32), HelperError> {
        self.authorize_sender(&header)?;

        if !is_known_command(&cmd) {
            return Err(HelperError::UnknownCommand(cmd));
        }
        for a in &args {
            if a.starts_with('-') {
                return Err(HelperError::InvalidArgument(format!(
                    "arguments may not start with '-': {a}"
                )));
            }
        }

        let (common, timeout, vin, connect_timeout_sec, command_timeout_sec) = {
            let cfg = self.cfg.lock().unwrap();
            let common = self.common_args_locked(&cfg)?;
            // i64 before the sum, not i32: an overflowing i32 sum here used
            // to be able to turn into a negative (already-cancelled)
            // deadline - a previously-fixed bug, preserved here.
            let secs = i64::from(cfg.connect_timeout_sec) + i64::from(cfg.command_timeout_sec) + 10;
            (
                common,
                Duration::from_secs(secs.cast_unsigned()),
                cfg.vin.clone(),
                cfg.connect_timeout_sec,
                cfg.command_timeout_sec,
            )
        };

        let _permit = self
            .ble_sem
            .try_lock()
            .map_err(|_| HelperError::Busy("another BLE command is in progress".to_string()))?;

        let mut command_argv = common;
        command_argv.push(cmd.clone());
        command_argv.extend(args.iter().cloned());

        if is_pin_command(&cmd) {
            eprintln!("teslacontrold: Run({cmd}, [{} redacted args])", args.len());
        } else {
            eprintln!("teslacontrold: Run({cmd}, {args:?})");
        }

        let outcome = match &self.session {
            Some(session) => {
                let key_path = self.private_key_path().to_string_lossy().into_owned();
                match session.run(
                    &cmd,
                    &args,
                    &vin,
                    &key_path,
                    connect_timeout_sec,
                    command_timeout_sec,
                    timeout,
                ) {
                    Ok(o) => RunOutcome {
                        ok: o.ok,
                        stdout: o.stdout,
                        stderr: o.stderr,
                        exit_code: o.exit_code,
                    },
                    Err(e) => {
                        eprintln!(
                            "teslacontrold: persistent session unavailable ({e}); falling back to one-shot tesla-control for {cmd}"
                        );
                        run_binary(&self.bin_dir, "tesla-control", &command_argv, timeout)
                    }
                }
            }
            None => run_binary(&self.bin_dir, "tesla-control", &command_argv, timeout),
        };

        Ok((
            outcome.ok,
            outcome.stdout,
            outcome.stderr,
            outcome.exit_code,
        ))
    }

    /// Creates a new local private key (file-backed - there is no Sailfish
    /// OS keyring backend) and returns its PEM-encoded public key.
    fn generate_key(
        &self,
        force: bool,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<(bool, String, String), HelperError> {
        self.authorize_sender(&header)?;
        // Holds the config mutex for the whole call, same as the original -
        // GenerateKey doesn't read Config, but this still serializes
        // concurrent key generation against writing the same key files.
        let _cfg = self.cfg.lock().unwrap();

        let mut args = vec![
            "-keyring-type".to_string(),
            "file".to_string(),
            "-key-file".to_string(),
            self.private_key_path().to_string_lossy().into_owned(),
            "-output".to_string(),
            self.public_key_path().to_string_lossy().into_owned(),
        ];
        if force {
            args.push("-f".to_string());
        }
        args.push("create".to_string());

        eprintln!("teslacontrold: GenerateKey(force={force})");
        let outcome = run_binary(
            &self.bin_dir,
            "tesla-keygen",
            &args,
            Duration::from_secs(15),
        );
        if !outcome.ok {
            return Ok((false, String::new(), outcome.stderr.trim().to_string()));
        }
        // A live persistent session (if any) loaded the private key into
        // memory at connect time - the file on disk just changed under it,
        // so it must reconnect rather than keep signing with a stale key.
        if let Some(session) = &self.session {
            session.invalidate();
        }
        match std::fs::read_to_string(self.public_key_path()) {
            Ok(pub_key) => Ok((true, pub_key, String::new())),
            Err(e) => Ok((false, String::new(), e.to_string())),
        }
    }

    /// Enrolls the current public key with the vehicle via BLE, requiring
    /// physical NFC-card approval at the center console (matches the
    /// official app's "add key" flow).
    fn pair(
        &self,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<(bool, String, String), HelperError> {
        self.authorize_sender(&header)?;

        let (common, timeout) = {
            let cfg = self.cfg.lock().unwrap();
            let common = self.common_args_locked(&cfg)?;
            // Same envelope as Run() (connect + command + 10s), plus a
            // flat 30s allowance for the physical NFC-card tap at the
            // center console this command waits for. Previously a
            // hardcoded 60s independent of the configured connect
            // timeout - fine at the 20s default (60s > 20+5+10=35s), but
            // could cut off a connect attempt that was still legitimately
            // in progress once connect_timeout_sec was raised anywhere
            // near its 300s max.
            let secs = i64::from(cfg.connect_timeout_sec) + i64::from(cfg.command_timeout_sec) + 10 + 30;
            (common, Duration::from_secs(secs.cast_unsigned()))
        };
        let pubkey_path = self.public_key_path();
        if std::fs::metadata(&pubkey_path).is_err() {
            return Ok((
                false,
                String::new(),
                "no public key on file; call GenerateKey first".to_string(),
            ));
        }

        let mut argv = common;
        argv.extend(
            [
                "add-key-request",
                pubkey_path.to_string_lossy().as_ref(),
                "owner",
                "cloud_key",
            ]
            .map(str::to_string),
        );

        eprintln!("teslacontrold: Pair()");
        // Unlike Run()'s Busy case, this is intentionally a normal (non-error)
        // reply, matching the original implementation.
        let Ok(_permit) = self.ble_sem.try_lock() else {
            return Ok((
                false,
                String::new(),
                "another BLE command is in progress".to_string(),
            ));
        };
        let outcome = run_binary(&self.bin_dir, "tesla-control", &argv, timeout);
        if !outcome.ok {
            return Ok((false, outcome.stdout, outcome.stderr.trim().to_string()));
        }
        Ok((true, outcome.stdout, String::new()))
    }

    fn set_config(
        &self,
        vin: &str,
        model: &str,
        key_name: &str,
        connect_timeout_sec: i32,
        command_timeout_sec: i32,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<(bool, String), HelperError> {
        self.authorize_sender(&header)?;

        let msg = match crate::config::validate_config(
            vin,
            model,
            key_name,
            connect_timeout_sec,
            command_timeout_sec,
        ) {
            Ok(()) => None,
            Err(e) => Some(e.to_string()),
        };
        if let Some(msg) = msg {
            return Ok((false, msg));
        }

        let mut cfg = self.cfg.lock().unwrap();
        cfg.vin = vin.trim().to_string();
        cfg.model = model.trim().to_ascii_lowercase();
        cfg.key_name = key_name.trim().to_string();
        cfg.connect_timeout_sec = connect_timeout_sec;
        cfg.command_timeout_sec = command_timeout_sec;
        if let Err(e) = cfg.save(&self.config_path()) {
            return Ok((false, e.to_string()));
        }
        eprintln!(
            "teslacontrold: SetConfig(vin={}, model={:?}, keyName={:?}, connectTimeout={}s, commandTimeout={}s)",
            cfg.vin, cfg.model, cfg.key_name, connect_timeout_sec, command_timeout_sec
        );
        // A live persistent session (if any) was spawned with the old
        // VIN/timeouts baked into its argv - drop it so the next Run()
        // spawns a fresh one with the new config instead of silently
        // continuing to talk to the previous vehicle/timeout settings.
        if let Some(session) = &self.session {
            session.invalidate();
        }
        Ok((true, String::new()))
    }

    fn get_config(
        &self,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<GetConfigReply, HelperError> {
        self.authorize_sender(&header)?;

        let cfg = self.cfg.lock().unwrap();
        let (has_key, pub_key) = match std::fs::read_to_string(self.public_key_path()) {
            Ok(data) => (true, data),
            Err(_) => (false, String::new()),
        };
        Ok((
            cfg.vin.clone(),
            cfg.model.clone(),
            cfg.key_name.clone(),
            cfg.connect_timeout_sec,
            cfg.command_timeout_sec,
            has_key,
            pub_key,
        ))
    }

    /// Returns the helper's own build version (`CARGO_PKG_VERSION`, stamped
    /// from the git tag by the release workflow, same as the app's
    /// `APP_VERSION`). The app shows both on the Settings page and flags a
    /// mismatch, so an app+helper installed from different releases stops
    /// being a silent failure mode (the 0.1.6 `model`-field interface break
    /// hid exactly that way). A helper built before this method existed -
    /// or, defensively, any compat break that removed it - answers
    /// `UnknownMethod`, which the client interprets as "helper older than
    /// this app".
    fn get_version(
        &self,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<String, HelperError> {
        self.authorize_sender(&header)?;
        Ok(env!("CARGO_PKG_VERSION").to_string())
    }
}

/// Execs a bundled binary with a hard deadline, returning combined exit
/// status. Never invoked with attacker-controlled binary names.
fn run_binary(bin_dir: &str, name: &str, args: &[String], timeout: Duration) -> RunOutcome {
    let path = Path::new(bin_dir).join(name);
    let Ok(mut child) = Command::new(&path)
        .args(args)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .inspect_err(|e| eprintln!("teslacontrold: failed to spawn {}: {e}", path.display()))
    else {
        return RunOutcome {
            ok: false,
            stdout: String::new(),
            stderr: String::new(),
            exit_code: -1,
        };
    };

    // Drain stdout/stderr on their own threads *before* waiting, so a child
    // that fills the OS pipe buffer can't deadlock us against wait_timeout.
    let mut stdout_pipe = child.stdout.take().expect("piped stdout");
    let mut stderr_pipe = child.stderr.take().expect("piped stderr");
    let stdout_thread = thread::spawn(move || {
        let mut buf = String::new();
        let _ = stdout_pipe.read_to_string(&mut buf);
        buf
    });
    let stderr_thread = thread::spawn(move || {
        let mut buf = String::new();
        let _ = stderr_pipe.read_to_string(&mut buf);
        buf
    });

    let wait_result = child.wait_timeout(timeout);

    let (ok, exit_code, timed_out) = match wait_result {
        Ok(Some(status)) => (status.success(), status.code().unwrap_or(-1), false),
        Ok(None) => {
            // Deadline exceeded: kill and reap, matching Go's
            // context.WithTimeout-triggered SIGKILL.
            let _ = child.kill();
            let _ = child.wait();
            (false, -1, true)
        }
        Err(_) => (false, -1, false),
    };

    let stdout = stdout_thread.join().unwrap_or_default();
    let mut stderr = stderr_thread.join().unwrap_or_default();
    if timed_out {
        stderr.push_str("\nteslacontrold: timed out waiting for tesla-control");
    }
    RunOutcome {
        ok,
        stdout,
        stderr,
        exit_code,
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn test_get_version_is_semver() {
        // The app compares this to its own APP_VERSION on the Settings page
        // and flags a mismatch, so a malformed/empty version would either
        // trip every install with a false warning or hide a real one.
        let v = env!("CARGO_PKG_VERSION");
        let parts: Vec<&str> = v.split('.').collect();
        assert_eq!(parts.len(), 3, "version {v:?} must be x.y.z");
        assert!(
            v.chars().all(|c| c.is_ascii_digit() || c == '.'),
            "version {v:?} must be digits and dots only"
        );
    }
}
