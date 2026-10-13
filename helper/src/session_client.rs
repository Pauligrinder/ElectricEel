//! Application session facade. Production uses the in-process protocol library;
//! the old subprocess backend exists only for standalone daemon compatibility
//! and its regression fixtures, and is not compiled into the Harbour app.
use crate::protocol_client::ProtocolClient;
use std::path::PathBuf;
use std::time::Duration;

#[derive(Clone, Debug, serde::Deserialize)]
pub(crate) struct SessionEvent {
    pub kind: String,
    #[serde(default)]
    pub vin: String,
    #[serde(default)]
    pub time: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub error_code: String,
    #[serde(skip)]
    pub generation: u64,
}

pub(crate) struct RunOutcome {
    pub ok: bool,
    pub stdout: String,
    pub stderr: String,
    pub exit_code: i32,
}

#[derive(Debug, thiserror::Error)]
pub(crate) enum SessionError {
    #[error("{0}")]
    Library(String),
    #[error("operation cancelled")]
    Cancelled,
}

pub struct SessionClient {
    inner: Implementation,
}
enum Implementation {
    Library(ProtocolClient),
    #[cfg(any(test, feature = "dbus"))]
    Legacy(Box<crate::legacy_session_client::SessionClient>),
}

impl SessionClient {
    #[must_use]
    pub fn library(path: PathBuf, state: PathBuf) -> Self {
        Self {
            inner: Implementation::Library(ProtocolClient::new(path, state)),
        }
    }
    #[cfg(any(test, feature = "dbus"))]
    #[must_use]
    pub fn new(path: PathBuf, backend: &str, state: PathBuf) -> Self {
        Self {
            inner: Implementation::Legacy(Box::new(
                crate::legacy_session_client::SessionClient::new(path, backend, state),
            )),
        }
    }
    pub(crate) fn invalidate(&self) {
        match &self.inner {
            Implementation::Library(client) => client.invalidate(),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.invalidate(),
        }
    }
    pub(crate) fn cancel(&self) {
        match &self.inner {
            Implementation::Library(client) => client.cancel(),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.cancel(),
        }
    }
    pub(crate) fn generation(&self) -> u64 {
        match &self.inner {
            Implementation::Library(client) => client.generation(),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.generation(),
        }
    }
    #[cfg(test)]
    pub(crate) fn is_alive(&self) -> bool {
        match &self.inner {
            Implementation::Library(client) => client.is_alive(),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.is_alive(),
        }
    }
    pub(crate) fn is_presence_active(&self) -> bool {
        match &self.inner {
            Implementation::Library(client) => client.is_presence_active(),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.is_presence_active(),
        }
    }
    pub(crate) fn ble_backend(&self) -> &str {
        match &self.inner {
            Implementation::Library(_) => "bluez",
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.ble_backend(),
        }
    }
    #[allow(clippy::too_many_arguments)]
    pub(crate) fn run(
        &self,
        cmd: &str,
        args: &[String],
        vin: &str,
        key: &str,
        connect: i32,
        command: i32,
        timeout: Duration,
    ) -> Result<RunOutcome, SessionError> {
        match &self.inner {
            Implementation::Library(client) => {
                client.run(cmd, args, vin, key, connect, command, timeout)
            }
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client
                .run(cmd, args, vin, key, connect, command, timeout)
                .map(convert)
                .map_err(|error| SessionError::Library(error.to_string())),
        }
    }
    #[allow(clippy::too_many_arguments)]
    pub(crate) fn keygen(
        &self,
        force: bool,
        key: &str,
        vin: &str,
        connect: i32,
        command: i32,
        timeout: Duration,
    ) -> Result<RunOutcome, SessionError> {
        let _ = (vin, connect, command, timeout);
        match &self.inner {
            Implementation::Library(client) => client.keygen(force, key),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client
                .keygen(force, key, vin, connect, command, timeout)
                .map(convert)
                .map_err(|error| SessionError::Library(error.to_string())),
        }
    }
    pub(crate) fn start_presence(
        &self,
        vin: &str,
        key: &str,
        connect: i32,
        command: i32,
        timeout: Duration,
    ) -> Result<RunOutcome, SessionError> {
        let _ = timeout;
        match &self.inner {
            Implementation::Library(client) => client.start_presence(vin, key, connect, command),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client
                .start_presence(vin, key, connect, command, timeout)
                .map(convert)
                .map_err(|error| SessionError::Library(error.to_string())),
        }
    }
    pub(crate) fn stop_presence(
        &self,
        vin: &str,
        key: &str,
        connect: i32,
        command: i32,
        timeout: Duration,
    ) -> Result<RunOutcome, SessionError> {
        let _ = (vin, key, connect, command, timeout);
        match &self.inner {
            Implementation::Library(client) => {
                client.stop_presence()?;
                Ok(RunOutcome {
                    ok: true,
                    stdout: String::new(),
                    stderr: String::new(),
                    exit_code: 0,
                })
            }
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client
                .stop_presence(vin, key, connect, command, timeout)
                .map(convert)
                .map_err(|error| SessionError::Library(error.to_string())),
        }
    }
    pub(crate) fn poll_event(&self) -> Option<SessionEvent> {
        match &self.inner {
            Implementation::Library(client) => client.poll_event(),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.poll_event().map(|event| SessionEvent {
                kind: event.kind,
                vin: event.vin,
                time: event.time,
                error: event.error,
                error_code: event.error_code,
                generation: event.generation,
            }),
        }
    }
    pub(crate) async fn wait_event(&self) {
        match &self.inner {
            Implementation::Library(client) => client.wait_event().await,
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.wait_event().await,
        }
    }
    pub(crate) fn clear_events(&self) {
        match &self.inner {
            Implementation::Library(client) => client.clear_events(),
            #[cfg(any(test, feature = "dbus"))]
            Implementation::Legacy(client) => client.clear_events(),
        }
    }
}

#[cfg(any(test, feature = "dbus"))]
fn convert(result: crate::legacy_session_client::RunOutcome) -> RunOutcome {
    RunOutcome {
        ok: result.ok,
        stdout: result.stdout,
        stderr: result.stderr,
        exit_code: result.exit_code,
    }
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use std::{path::Path, sync::mpsc, thread};
    pub(crate) fn accept_mock(
        path: &Path,
        greet: Vec<String>,
        replies: Vec<String>,
    ) -> (
        SessionClient,
        mpsc::Receiver<String>,
        thread::JoinHandle<()>,
    ) {
        let (client, got, peer) =
            crate::legacy_session_client::tests::accept_mock(path, greet, replies);
        (
            SessionClient {
                inner: Implementation::Legacy(Box::new(client)),
            },
            got,
            peer,
        )
    }
    pub(crate) fn attach_mock(
        client: &SessionClient,
        greet: Vec<String>,
        replies: Vec<String>,
    ) -> (mpsc::Receiver<String>, thread::JoinHandle<()>) {
        let Implementation::Legacy(client) = &client.inner else {
            panic!("legacy fixture")
        };
        crate::legacy_session_client::tests::attach_mock(client, greet, replies)
    }
    pub(crate) fn accept_blocked_mock(
        path: &Path,
    ) -> (
        SessionClient,
        mpsc::Receiver<String>,
        thread::JoinHandle<()>,
    ) {
        let (client, got, peer) = crate::legacy_session_client::tests::accept_blocked_mock(path);
        (
            SessionClient {
                inner: Implementation::Legacy(Box::new(client)),
            },
            got,
            peer,
        )
    }
}
