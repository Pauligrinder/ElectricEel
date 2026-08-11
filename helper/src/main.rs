mod authorize;
mod commands;
mod config;
mod error;
mod helper;
mod session_client;

use std::path::Path;

use helper::{Helper, IFACE_NAME};
use session_client::SessionClient;

const BUS_NAME: &str = "org.teslacontrol.Helper";
const OBJECT_PATH: &str = "/org/teslacontrol/Helper";

/// binDir holds the bundled, setcap'd tesla-control/tesla-keygen binaries.
/// stateDir holds the private key and persisted config; both are created by
/// the RPM with restrictive permissions for the service's own system user.
fn env_or(key: &str, default: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| default.to_string())
}

fn env_flag(key: &str) -> bool {
    match std::env::var(key) {
        Ok(v) => v != "0" && !v.eq_ignore_ascii_case("false"),
        Err(_) => false,
    }
}

fn main() {
    let bin_dir = env_or("TESLACONTROLD_BIN_DIR", "/opt/teslacontrold/bin");
    let state_dir = env_or("TESLACONTROLD_STATE_DIR", "/var/lib/teslacontrold");
    let allowed_callers = authorize::default_allowed_callers();

    if let Err(e) = std::fs::create_dir_all(&state_dir) {
        eprintln!("cannot create state dir {state_dir}: {e}");
        std::process::exit(1);
    }

    let credentials_conn = match zbus::blocking::Connection::system() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("cannot connect to system bus: {e}");
            std::process::exit(1);
        }
    };

    // Off by default and not yet verified on real hardware - see
    // KNOWN_ISSUES.md. tesla-session is bundled in bin_dir alongside
    // tesla-control/tesla-keygen and setcap'd the same way (needs
    // CAP_NET_ADMIN itself once it's the one holding the BLE session).
    let session = if env_flag("TESLACONTROLD_PERSISTENT_SESSION") {
        eprintln!(
            "teslacontrold: persistent BLE session enabled (TESLACONTROLD_PERSISTENT_SESSION)"
        );
        Some(SessionClient::new(Path::new(&bin_dir).join("tesla-session")))
    } else {
        None
    };

    let helper = Helper::new(bin_dir, state_dir, allowed_callers, credentials_conn, session);

    let conn = zbus::blocking::connection::Builder::system()
        .and_then(|b| b.serve_at(OBJECT_PATH, helper))
        .and_then(|b| b.name(BUS_NAME))
        .and_then(zbus::blocking::connection::Builder::build);

    // Named _conn, not conn: it's never read again, only kept alive (its
    // Drop would tear down the background dispatch machinery) until the
    // process exits at thread::park() below. The leading underscore tells
    // rustc that's deliberate instead of needing a `let _ = &conn;` no-op
    // to silence the unused-variable lint.
    let _conn = match conn {
        Ok(c) => c,
        Err(e) => {
            eprintln!("cannot start teslacontrold: {e}");
            std::process::exit(1);
        }
    };

    eprintln!("teslacontrold listening on {BUS_NAME} {OBJECT_PATH} ({IFACE_NAME})");
    std::thread::park();
}
