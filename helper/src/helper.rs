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

pub const IFACE_NAME: &str = "org.teslacontrol.Helper1";

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
    /// A connection dedicated to outbound proxy calls (GetConnectionCredentials
    /// in authorize()), deliberately separate from the connection the
    /// ObjectServer uses to dispatch these very method calls. zbus's own
    /// docs warn against blocking-API calls from within an interface method
    /// on the *same* connection that's driving that method's dispatch (the
    /// "async sandwich" footgun) - a second, independent connection/socket
    /// avoids that reentrancy hazard entirely.
    credentials_conn: zbus::blocking::Connection,
}

impl Helper {
    pub fn new(
        bin_dir: String,
        state_dir: String,
        allowed_callers: Vec<String>,
        credentials_conn: zbus::blocking::Connection,
    ) -> Helper {
        let config_path = Path::new(&state_dir).join("config.json");
        let cfg = match Config::load(&config_path) {
            Ok(cfg) => cfg,
            Err(e) => {
                eprintln!("teslacontrold: cannot read config {config_path:?}: {e}");
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

        let (common, timeout) = {
            let cfg = self.cfg.lock().unwrap();
            let common = self.common_args_locked(&cfg)?;
            // i64 before the sum, not i32: an overflowing i32 sum here used
            // to be able to turn into a negative (already-cancelled)
            // deadline - a previously-fixed bug, preserved here.
            let secs = cfg.connect_timeout_sec as i64 + cfg.command_timeout_sec as i64 + 10;
            (common, Duration::from_secs(secs as u64))
        };

        let _permit = self
            .ble_sem
            .try_lock()
            .map_err(|_| HelperError::Busy("another BLE command is in progress".to_string()))?;

        let mut argv = common;
        argv.push(cmd.clone());
        argv.extend(args.iter().cloned());

        if is_pin_command(&cmd) {
            eprintln!("teslacontrold: Run({cmd}, [{} redacted args])", args.len());
        } else {
            eprintln!("teslacontrold: Run({cmd}, {args:?})");
        }
        let outcome = run_binary(&self.bin_dir, "tesla-control", &argv, timeout);
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

        let common = {
            let cfg = self.cfg.lock().unwrap();
            self.common_args_locked(&cfg)?
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
        let _permit = match self.ble_sem.try_lock() {
            Ok(permit) => permit,
            Err(_) => {
                return Ok((
                    false,
                    String::new(),
                    "another BLE command is in progress".to_string(),
                ))
            }
        };
        let outcome = run_binary(
            &self.bin_dir,
            "tesla-control",
            &argv,
            Duration::from_secs(60),
        );
        if !outcome.ok {
            return Ok((false, outcome.stdout, outcome.stderr.trim().to_string()));
        }
        Ok((true, outcome.stdout, String::new()))
    }

    fn set_config(
        &self,
        vin: String,
        key_name: String,
        connect_timeout_sec: i32,
        command_timeout_sec: i32,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<(bool, String), HelperError> {
        self.authorize_sender(&header)?;

        let msg =
            match crate::config::validate_config(&vin, connect_timeout_sec, command_timeout_sec) {
                Ok(()) => None,
                Err(e) => Some(e.to_string()),
            };
        if let Some(msg) = msg {
            return Ok((false, msg));
        }

        let mut cfg = self.cfg.lock().unwrap();
        cfg.vin = vin.trim().to_string();
        cfg.key_name = key_name.trim().to_string();
        cfg.connect_timeout_sec = connect_timeout_sec;
        cfg.command_timeout_sec = command_timeout_sec;
        if let Err(e) = cfg.save(&self.config_path()) {
            return Ok((false, e.to_string()));
        }
        eprintln!(
            "teslacontrold: SetConfig(vin={}, keyName={:?}, connectTimeout={}s, commandTimeout={}s)",
            cfg.vin, cfg.key_name, connect_timeout_sec, command_timeout_sec
        );
        Ok((true, String::new()))
    }

    fn get_config(
        &self,
        #[zbus(header)] header: Header<'_>,
    ) -> Result<(String, String, i32, i32, bool, String), HelperError> {
        self.authorize_sender(&header)?;

        let cfg = self.cfg.lock().unwrap();
        let (has_key, pub_key) = match std::fs::read_to_string(self.public_key_path()) {
            Ok(data) => (true, data),
            Err(_) => (false, String::new()),
        };
        Ok((
            cfg.vin.clone(),
            cfg.key_name.clone(),
            cfg.connect_timeout_sec,
            cfg.command_timeout_sec,
            has_key,
            pub_key,
        ))
    }
}

/// Execs a bundled binary with a hard deadline, returning combined exit
/// status. Never invoked with attacker-controlled binary names.
fn run_binary(bin_dir: &str, name: &str, args: &[String], timeout: Duration) -> RunOutcome {
    let path = Path::new(bin_dir).join(name);
    let mut child = match Command::new(&path)
        .args(args)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
    {
        Ok(c) => c,
        // The original silently drops the spawn error here too - preserved
        // for parity rather than "improved" without being asked.
        Err(_) => {
            return RunOutcome {
                ok: false,
                stdout: String::new(),
                stderr: String::new(),
                exit_code: -1,
            }
        }
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
