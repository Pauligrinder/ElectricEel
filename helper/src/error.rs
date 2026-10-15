//! Error types of the in-process control core (`core.rs`).

use std::io;

use crate::config::ConfigError;

/// Errors from the soft-fail core operations (`set_config`, `generate_key`,
/// `start_phone_key`): the operation did not proceed, but the failure is
/// delivered to the caller as `ok=false` + a message rather than as a
/// [`HelperError`]. Typed so the known cases can be matched directly and the
/// underlying `io`/config cause is kept in the `source()` chain.
#[derive(Debug, thiserror::Error)]
pub(crate) enum OperationError {
    /// Presence can't start: no configured VIN, or the current key hasn't
    /// completed NFC pairing with it.
    #[error("phone key is waiting for a configured VIN and completed pairing")]
    NotPaired,
    /// Presence needs the protocol library, which isn't available.
    #[error("persistent BLE session is unavailable")]
    NoSession,
    /// Presence could not start because the Bluetooth adapter is off.
    #[error("{0}")]
    BluetoothOff(String),
    /// The `BlueZ` presence service refused to start (holds its error).
    #[error("{0}")]
    PresenceFailed(String),
    /// Key generation was refused or failed (holds the reason).
    #[error("{0}")]
    KeygenFailed(String),
    /// The `SetConfig` payload failed validation.
    #[error("{0}")]
    Config(#[from] ConfigError),
    /// `config.json` could not be persisted.
    #[error("could not persist config: {0}")]
    Persist(#[source] io::Error),
}

/// Hard failures that abort a call and surface to the UI as an error message
/// rather than as a soft `ok=false` reply.
#[derive(Debug, thiserror::Error)]
pub enum HelperError {
    #[error("busy: {0}")]
    Busy(String),
    #[error("not configured: {0}")]
    NotConfigured(String),
    #[error("no key: {0}")]
    NoKey(String),
    #[error("unknown command: {0}")]
    UnknownCommand(String),
    #[error("invalid argument: {0}")]
    InvalidArgument(String),
    /// The protocol library or its BLE session failed.
    #[error("session unavailable: {0}")]
    SessionUnavailable(String),
}
