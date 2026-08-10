mod authorize;
mod commands;
mod config;
mod error;
mod helper;

use helper::{Helper, IFACE_NAME};

const BUS_NAME: &str = "org.teslacontrol.Helper";
const OBJECT_PATH: &str = "/org/teslacontrol/Helper";

/// binDir holds the bundled, setcap'd tesla-control/tesla-keygen binaries.
/// stateDir holds the private key and persisted config; both are created by
/// the RPM with restrictive permissions for the service's own system user.
fn env_or(key: &str, default: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| default.to_string())
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

    let helper = Helper::new(bin_dir, state_dir, allowed_callers, credentials_conn);

    let conn = zbus::blocking::connection::Builder::system()
        .and_then(|b| b.serve_at(OBJECT_PATH, helper))
        .and_then(|b| b.name(BUS_NAME))
        .and_then(|b| b.build());

    let conn = match conn {
        Ok(c) => c,
        Err(e) => {
            eprintln!("cannot start teslacontrold: {e}");
            std::process::exit(1);
        }
    };

    eprintln!("teslacontrold listening on {BUS_NAME} {OBJECT_PATH} ({IFACE_NAME})");

    // Keep the connection (and its background dispatch machinery) alive for
    // the life of the process; there's nothing else for main to do.
    let _ = &conn;
    std::thread::park();
}
