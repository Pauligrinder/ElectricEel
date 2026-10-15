//! Rust-owned phone-key controller and operation orchestration.
use crate::{
    phone_key::Proximity,
    session_client::{RunOutcome, SessionError, SessionEvent},
    tesla_ble::{self, Domain, Operation, Session, Status},
};
use p256::elliptic_curve::sec1::ToEncodedPoint;
use serde::Deserialize;
use std::{
    collections::VecDeque,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, AtomicU64, Ordering},
        Arc, Mutex,
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

#[derive(Deserialize)]
struct BodyControllerState {
    #[serde(default, rename = "userPresence")]
    user_presence: UserPresence,
}

#[derive(Debug, Default, PartialEq, Eq, Deserialize)]
enum UserPresence {
    #[default]
    #[serde(rename = "VEHICLE_USER_PRESENCE_UNKNOWN")]
    Unknown,
    #[serde(rename = "VEHICLE_USER_PRESENCE_NOT_PRESENT")]
    NotPresent,
    #[serde(rename = "VEHICLE_USER_PRESENCE_PRESENT")]
    Present,
}

fn user_present(payload: &[u8]) -> bool {
    serde_json::from_slice::<BodyControllerState>(payload)
        .is_ok_and(|state| state.user_presence == UserPresence::Present)
}

fn seconds(value: i32) -> Duration {
    Duration::from_secs(u64::try_from(value.max(1)).unwrap_or(1))
}
fn failure(error: impl std::fmt::Display) -> SessionError {
    SessionError::Library(error.to_string())
}
fn outcome(result: tesla_ble::Outcome) -> RunOutcome {
    RunOutcome {
        ok: result.status == Status::Ok,
        stdout: String::from_utf8_lossy(&result.payload).into_owned(),
        stderr: result.error,
        exit_code: i32::from(result.status != Status::Ok),
    }
}
fn execute(
    session: &Session,
    operation: Operation,
    timeout: Duration,
) -> Result<tesla_ble::Outcome, SessionError> {
    async_global_executor::block_on(session.execute(operation, timeout)).map_err(failure)
}

struct Inner {
    path: PathBuf,
    state: PathBuf,
    library: Mutex<Option<Arc<tesla_ble::Library>>>,
    session: Mutex<Option<(String, String, Arc<Session>)>>,
    operation: async_lock::Mutex<()>,
    generation: AtomicU64,
    mode: AtomicBool,
    cancelled: AtomicBool,
    connected: AtomicBool,
    proximity: Mutex<Proximity>,
    last_command: Mutex<Option<Instant>>,
    events: Mutex<VecDeque<SessionEvent>>,
    terminal: Mutex<Option<SessionEvent>>,
    ready: async_channel::Sender<()>,
    wait: async_channel::Receiver<()>,
}

pub(crate) struct ProtocolClient {
    inner: Arc<Inner>,
    controller: Mutex<Option<JoinHandle<()>>>,
    maintenance: Mutex<Option<JoinHandle<()>>>,
}

impl ProtocolClient {
    pub fn new(path: PathBuf, state: PathBuf) -> Self {
        let (ready, wait) = async_channel::bounded(1);
        Self {
            inner: Arc::new(Inner {
                path,
                state,
                library: Mutex::new(None),
                session: Mutex::new(None),
                operation: async_lock::Mutex::new(()),
                generation: AtomicU64::new(1),
                mode: AtomicBool::new(false),
                cancelled: AtomicBool::new(false),
                connected: AtomicBool::new(false),
                proximity: Mutex::new(Proximity::default()),
                last_command: Mutex::new(None),
                events: Mutex::new(VecDeque::new()),
                terminal: Mutex::new(None),
                ready,
                wait,
            }),
            controller: Mutex::new(None),
            maintenance: Mutex::new(None),
        }
    }
    fn ensure(&self, vin: &str, key: &str) -> Result<Arc<Session>, SessionError> {
        if self.inner.cancelled.load(Ordering::SeqCst) {
            return Err(SessionError::Cancelled);
        }
        let mut slot = self
            .inner
            .session
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if let Some((old_vin, old_key, session)) = &*slot {
            if old_vin == vin && old_key == key && !session.stopped() && !session.faulted() {
                return Ok(Arc::clone(session));
            }
            session.stop();
        }
        drop(slot.take());
        let mut library = self
            .inner
            .library
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if library.is_none() {
            // SAFETY: app-owned fixed library path, never supplied through UI.
            *library =
                Some(unsafe { tesla_ble::Library::load(&self.inner.path) }.map_err(failure)?);
        }
        let signing = crate::keys::load(Path::new(key)).map_err(failure)?;
        let scalar = zeroize::Zeroizing::new(<[u8; 32]>::from(signing.to_bytes()));
        let session = Arc::new(
            Session::new(
                Arc::clone(library.as_ref().ok_or_else(|| failure("missing library"))?),
                vin,
                "",
                &scalar,
            )
            .map_err(failure)?,
        );
        *slot = Some((vin.into(), key.into(), Arc::clone(&session)));
        self.inner.connected.store(false, Ordering::SeqCst);
        let inner = Arc::clone(&self.inner);
        let session_for_worker = Arc::clone(&session);
        let mut worker = self
            .maintenance
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if let Some(old) = worker.take() {
            let _ = old.join();
        }
        *worker = Some(thread::spawn(move || {
            maintenance(&inner, &session_for_worker);
        }));
        Ok(session)
    }
    pub fn generation(&self) -> u64 {
        self.inner.generation.load(Ordering::SeqCst)
    }
    pub fn is_alive(&self) -> bool {
        self.inner
            .session
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .as_ref()
            .is_some_and(|(_, _, session)| !session.stopped() && !session.faulted())
    }
    pub fn is_presence_active(&self) -> bool {
        self.inner.mode.load(Ordering::SeqCst) && self.is_alive()
    }
    pub fn cancel(&self) {
        self.inner.cancelled.store(true, Ordering::SeqCst);
        self.inner.mode.store(false, Ordering::SeqCst);
        if let Some((_, _, session)) = &*self
            .inner
            .session
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
        {
            session.stop();
        }
        let _ = self.inner.ready.try_send(());
    }
    pub fn invalidate(&self) {
        self.inner.mode.store(false, Ordering::SeqCst);
        if let Some((_, _, session)) = &*self
            .inner
            .session
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
        {
            session.stop();
        }
        self.join_controller();
        if let Some(worker) = self
            .maintenance
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take()
        {
            let _ = worker.join();
        }
        let _operation = self.inner.operation.lock_blocking();
        self.inner
            .session
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take();
        self.inner.generation.fetch_add(1, Ordering::SeqCst);
        self.inner.connected.store(false, Ordering::SeqCst);
        self.clear_events();
        *self
            .inner
            .proximity
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = Proximity::default();
    }
    fn join_controller(&self) {
        if let Some(worker) = self
            .controller
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take()
        {
            let _ = worker.join();
        }
    }
    pub fn keygen(&self, force: bool, key: &str) -> Result<RunOutcome, SessionError> {
        let public = self.inner.state.join("public_key.pem");
        let pem = crate::keys::generate(Path::new(key), &public, force).map_err(failure)?;
        Ok(RunOutcome {
            ok: true,
            stdout: pem,
            stderr: String::new(),
            exit_code: 0,
        })
    }
    pub fn start_presence(
        &self,
        vin: &str,
        key: &str,
        connect: i32,
        command: i32,
    ) -> Result<RunOutcome, SessionError> {
        if self.is_presence_active() {
            return Ok(success());
        }
        self.join_controller();
        let session = self.ensure(vin, key)?;
        let _operation = self.inner.operation.lock_blocking();
        let result = execute(&session, Operation::WatchStart, seconds(connect))?;
        if result.status != Status::Ok {
            return Ok(outcome(result));
        }
        let result = execute(&session, Operation::PassiveEntry(true), seconds(command))?;
        if result.status != Status::Ok {
            return Ok(outcome(result));
        }
        self.inner.mode.store(true, Ordering::SeqCst);
        let inner = Arc::clone(&self.inner);
        let vin = vin.to_string();
        let generation = self.generation();
        *self
            .controller
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(thread::spawn(move || {
            controller(
                &inner,
                &session,
                &vin,
                generation,
                seconds(connect),
                seconds(command),
            );
        }));
        Ok(success())
    }
    pub fn stop_presence(&self) -> Result<(), SessionError> {
        self.inner.mode.store(false, Ordering::SeqCst);
        if let Some((_, _, session)) = &*self
            .inner
            .session
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
        {
            session.cancel_operations();
        }
        self.join_controller();
        let _operation = self.inner.operation.lock_blocking();
        if let Some((_, _, session)) = &*self
            .inner
            .session
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
        {
            if !session.stopped() {
                for operation in [
                    Operation::PassiveEntry(false),
                    Operation::WatchStop,
                    Operation::Disconnect,
                ] {
                    let result = execute(session, operation, Duration::from_secs(5))?;
                    if result.status != Status::Ok {
                        return Err(failure(result.error));
                    }
                }
            }
        }
        self.inner.connected.store(false, Ordering::SeqCst);
        *self
            .inner
            .proximity
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = Proximity::default();
        Ok(())
    }
    #[allow(clippy::too_many_arguments)]
    pub fn run(
        &self,
        cmd: &str,
        args: &[String],
        vin: &str,
        key: &str,
        connect: i32,
        command: i32,
        _timeout: Duration,
    ) -> Result<RunOutcome, SessionError> {
        if cmd == "pair" {
            return self.enroll(args, vin, key, connect, command, true);
        }
        if cmd == "add-key-request" {
            let restart = self.is_presence_active();
            let result = self.enroll(args, vin, key, connect, command, false);
            if restart && !self.inner.cancelled.load(Ordering::SeqCst) {
                match self.start_presence(vin, key, connect, command) {
                    Ok(outcome) if outcome.ok => emit(
                        &self.inner,
                        vin,
                        self.generation(),
                        "presence_restarted",
                        "",
                        Status::Ok,
                    ),
                    outcome => {
                        let error = match outcome {
                            Ok(outcome) => outcome.stderr,
                            Err(error) => error.to_string(),
                        };
                        emit(
                            &self.inner,
                            vin,
                            self.generation(),
                            "presence_stopped",
                            &error,
                            Status::Failed,
                        );
                    }
                }
            }
            return result;
        }
        let operation = crate::protocol_commands::parse(cmd, args).map_err(failure)?;
        let _operation = self.inner.operation.lock_blocking();
        let session = self.ensure(vin, key)?;
        if self.inner.mode.load(Ordering::SeqCst)
            && !self.inner.connected.load(Ordering::SeqCst)
            && !self
                .inner
                .proximity
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .can_connect(Instant::now())
        {
            return Err(failure(
                "vehicle not nearby; wait for phone key to connect and retry",
            ));
        }
        let domain = if cmd == "add-key-request" || cmd == "session-info" {
            Domain::Unauthenticated
        } else if [
            "lock",
            "unlock",
            "drive",
            "wake",
            "body-controller-state",
            "add-key",
            "remove-key",
            "list-keys",
            "trunk-open",
            "trunk-move",
            "trunk-close",
            "frunk-open",
            "tonneau-open",
            "tonneau-close",
            "tonneau-stop",
            "autosecure-modelx",
        ]
        .contains(&cmd)
        {
            Domain::Vcsec
        } else {
            Domain::Infotainment
        };
        let result = execute(&session, Operation::Connect(domain), seconds(connect))?;
        if result.status != Status::Ok {
            self.inner.connected.store(false, Ordering::SeqCst);
            return Ok(outcome(result));
        }
        self.inner.connected.store(true, Ordering::SeqCst);
        let result = execute(&session, operation, seconds(command))?;
        if [
            Status::Uncertain,
            Status::Cancelled,
            Status::TimedOut,
            Status::NotConnected,
        ]
        .contains(&result.status)
        {
            self.inner.connected.store(false, Ordering::SeqCst);
        }
        *self
            .inner
            .last_command
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(Instant::now());
        Ok(outcome(result))
    }
    fn enroll(
        &self,
        args: &[String],
        vin: &str,
        key: &str,
        connect: i32,
        command: i32,
        own_key: bool,
    ) -> Result<RunOutcome, SessionError> {
        if args.len() != 3 {
            return Err(failure("pair requires key, role and form factor"));
        }
        let public = crate::keys::public_bytes(Path::new(&args[0])).map_err(failure)?;
        if own_key
            && public
                != crate::keys::load(Path::new(key))
                    .map_err(failure)?
                    .public_key()
                    .to_encoded_point(false)
                    .as_bytes()
        {
            return Err(failure("pairing public key does not match private key"));
        }
        self.stop_presence()?;
        let _operation = self.inner.operation.lock_blocking();
        let session = self.ensure(vin, key)?;
        // Never retain a connection established for unauthenticated enrollment,
        // regardless of rejection, deadline, cancellation or verification result.
        let _disconnect = DisconnectOnDrop {
            session: Arc::clone(&session),
        };
        let result = execute(
            &session,
            Operation::Connect(Domain::Unauthenticated),
            seconds(connect),
        )?;
        if result.status != Status::Ok {
            return Ok(outcome(result));
        }
        let request = crate::protocol_commands::parse("add-key-request", args).map_err(failure)?;
        let sent = execute(&session, request, seconds(command))?;
        if sent.status != Status::Ok {
            return Ok(outcome(sent));
        }
        let deadline = Instant::now() + Duration::from_secs(90);
        loop {
            if self.inner.cancelled.load(Ordering::SeqCst) {
                return Err(SessionError::Cancelled);
            }
            let result = execute(
                &session,
                Operation::EnrollmentStatus(public.clone()),
                Duration::from_secs(3).min(deadline.saturating_duration_since(Instant::now())),
            )?;
            if result.status == Status::Ok {
                let _ = execute(&session, Operation::Disconnect, Duration::from_secs(5));
                return Ok(outcome(result));
            }
            if Instant::now() >= deadline {
                let _ = execute(&session, Operation::Disconnect, Duration::from_secs(5));
                return Err(failure("NFC enrollment was not confirmed"));
            }
            for _ in 0..10 {
                if self.inner.cancelled.load(Ordering::SeqCst) {
                    return Err(SessionError::Cancelled);
                }
                thread::sleep(Duration::from_millis(100));
            }
        }
    }
    pub fn poll_event(&self) -> Option<SessionEvent> {
        if let Some(event) = self
            .inner
            .terminal
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take()
        {
            return Some(event);
        }
        self.inner
            .events
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .pop_front()
    }
    pub async fn wait_event(&self) {
        let _ = self.inner.wait.recv().await;
    }
    pub fn clear_events(&self) {
        self.inner
            .terminal
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take();
        self.inner
            .events
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .clear();
        while self.inner.wait.try_recv().is_ok() {}
    }
}
struct DisconnectOnDrop {
    session: Arc<Session>,
}
impl Drop for DisconnectOnDrop {
    fn drop(&mut self) {
        if !self.session.stopped() {
            let _ = execute(&self.session, Operation::Disconnect, Duration::from_secs(5));
        }
    }
}
impl Drop for ProtocolClient {
    fn drop(&mut self) {
        self.cancel();
        self.invalidate();
    }
}
fn success() -> RunOutcome {
    RunOutcome {
        ok: true,
        stdout: String::new(),
        stderr: String::new(),
        exit_code: 0,
    }
}
fn emit(inner: &Inner, vin: &str, generation: u64, kind: &str, error: &str, status: Status) {
    let event = SessionEvent {
        kind: kind.into(),
        vin: vin.into(),
        time: crate::keylog::utc_stamp(),
        error: error.into(),
        error_code: if status == Status::BluetoothOff {
            "bluetooth-off".into()
        } else {
            String::new()
        },
        generation,
    };
    if kind == "presence_stopped" {
        *inner
            .terminal
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(event);
        inner
            .events
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .clear();
        let _ = inner.ready.try_send(());
        return;
    }
    let mut events = inner
        .events
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    // Coalesce derived state snapshots, never command completions. A terminal
    // lifecycle event has its own priority slot and cannot be evicted.
    events.retain(|old| old.kind != event.kind);
    events.push_back(event);
    drop(events);
    let _ = inner.ready.try_send(());
}

#[allow(clippy::too_many_lines)] // Single owner for the phone-key transition loop.
fn controller(
    inner: &Arc<Inner>,
    session: &Arc<Session>,
    vin: &str,
    generation: u64,
    connect: Duration,
    command: Duration,
) {
    let mut connected_at = None;
    let mut status_at = None;
    let mut present = false;
    let mut inside = false;
    while inner.mode.load(Ordering::SeqCst)
        && !inner.cancelled.load(Ordering::SeqCst)
        && generation == inner.generation.load(Ordering::SeqCst)
    {
        if session.faulted() || session.stopped() {
            session.stop();
            emit(
                inner,
                vin,
                generation,
                "presence_stopped",
                "protocol session faulted",
                Status::InternalBug,
            );
            break;
        }
        while let Some(event) = session.poll_observation() {
            match event.kind {
                1 => inner
                    .proximity
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner)
                    .advertisement(Instant::now(), event.target, event.rssi),
                2 => {
                    inner.connected.store(false, Ordering::SeqCst);
                    connected_at = None;
                    status_at = None;
                    inside = false;
                    emit(
                        inner,
                        vin,
                        generation,
                        "presence_disconnected",
                        &event.error,
                        event.status,
                    );
                }
                3 => emit(inner, vin, generation, "presence_auth_ok", "", event.status),
                4 => emit(
                    inner,
                    vin,
                    generation,
                    "presence_auth_failed",
                    &event.error,
                    event.status,
                ),
                5 => {
                    emit(
                        inner,
                        vin,
                        generation,
                        "presence_error",
                        &event.error,
                        event.status,
                    );
                    session.stop();
                    emit(
                        inner,
                        vin,
                        generation,
                        "presence_stopped",
                        &event.error,
                        event.status,
                    );
                    inner.mode.store(false, Ordering::SeqCst);
                    return;
                }
                _ => (),
            }
        }
        let connected = inner.connected.load(Ordering::SeqCst);
        let now = Instant::now();
        let (depart, can_connect, target) = {
            let mut proximity = inner
                .proximity
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            (
                proximity.tick(now, connected),
                proximity.can_connect(now),
                proximity.target,
            )
        };
        if let Some(_operation) = inner.operation.try_lock() {
            if depart {
                let _ = execute(session, Operation::Disconnect, command);
                emit(inner, vin, generation, "presence_far", "", Status::Ok);
            }
            if !connected && can_connect {
                match execute(
                    session,
                    Operation::ConnectTarget {
                        domain: Domain::Vcsec,
                        target,
                    },
                    connect,
                ) {
                    Ok(result) if result.status == Status::Ok => {
                        inner.connected.store(true, Ordering::SeqCst);
                        inner
                            .proximity
                            .lock()
                            .unwrap_or_else(std::sync::PoisonError::into_inner)
                            .connected();
                        connected_at = Some(Instant::now());
                        status_at = None;
                        inside = false;
                        emit(inner, vin, generation, "presence_near", "", Status::Ok);
                    }
                    result => {
                        inner
                            .proximity
                            .lock()
                            .unwrap_or_else(std::sync::PoisonError::into_inner)
                            .failed(Instant::now());
                        let (status, error) = match result {
                            Ok(result) => (result.status, result.error),
                            Err(error) => (Status::Failed, error.to_string()),
                        };
                        emit(inner, vin, generation, "presence_error", &error, status);
                    }
                }
            }
            if inner.connected.load(Ordering::SeqCst)
                && status_at.is_none_or(|at: Instant| at.elapsed() >= Duration::from_secs(15))
            {
                match execute(session, Operation::BodyControllerState, command) {
                    Ok(result) if result.status == Status::Ok => {
                        present = user_present(&result.payload);
                        status_at = Some(Instant::now());
                        if connected_at.is_none() {
                            connected_at = Some(Instant::now());
                        }
                    }
                    result => {
                        present = false;
                        status_at = Some(Instant::now());
                        if let Ok(result) = result {
                            if [
                                Status::Uncertain,
                                Status::Cancelled,
                                Status::TimedOut,
                                Status::NotConnected,
                            ]
                            .contains(&result.status)
                            {
                                inner.connected.store(false, Ordering::SeqCst);
                                connected_at = None;
                                inside = false;
                                emit(
                                    inner,
                                    vin,
                                    generation,
                                    "presence_disconnected",
                                    &result.error,
                                    result.status,
                                );
                            }
                        }
                    }
                }
            }
        }
        if !inside
            && connected_at
                .zip(status_at)
                .is_some_and(|(connected, status)| {
                    crate::phone_key::inside(connected, status, Instant::now(), present)
                })
        {
            inside = true;
            emit(inner, vin, generation, "presence_inside", "", Status::Ok);
        }
        thread::sleep(Duration::from_millis(100));
    }
    inner.mode.store(false, Ordering::SeqCst);
}

fn maintenance(inner: &Arc<Inner>, session: &Arc<Session>) {
    let mut lost = 0;
    while !inner.cancelled.load(Ordering::SeqCst) && !session.stopped() {
        while let Some(message) = session.poll_diagnostic() {
            crate::keylog::log("go", &message);
        }
        let current = session.lost_diagnostics();
        if current != lost {
            crate::keylog::log(
                "go",
                &format!(
                    "diagnostic queue overflow: {} records dropped",
                    current - lost
                ),
            );
            lost = current;
        }
        if !inner.mode.load(Ordering::SeqCst)
            && inner
                .last_command
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .is_some_and(|at| at.elapsed() >= Duration::from_secs(90))
        {
            if let Some(_operation) = inner.operation.try_lock() {
                let _ = execute(session, Operation::Disconnect, Duration::from_secs(5));
                inner.connected.store(false, Ordering::SeqCst);
                *inner
                    .last_command
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner) = None;
            }
        }
        thread::sleep(Duration::from_millis(100));
    }
    while let Some(message) = session.poll_diagnostic() {
        crate::keylog::log("go", &message);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn presence_payload_validates_protocol_enum_and_decodes_escapes() {
        let plain =
            br#"{"userPresence":"VEHICLE_USER_PRESENCE_PRESENT","ignored":{"nested":[1,true]}}"#;
        assert!(user_present(plain));

        let escaped = br#"{"userPresence":"VEHICLE_USER_PRESENCE_\u0050RESENT"}"#;
        assert!(user_present(escaped));

        for (name, expected) in [
            ("VEHICLE_USER_PRESENCE_UNKNOWN", UserPresence::Unknown),
            (
                "VEHICLE_USER_PRESENCE_NOT_PRESENT",
                UserPresence::NotPresent,
            ),
            ("VEHICLE_USER_PRESENCE_PRESENT", UserPresence::Present),
        ] {
            let payload = format!(r#"{{"userPresence":"{name}"}}"#);
            let state: BodyControllerState = serde_json::from_str(&payload).unwrap();
            assert_eq!(state.user_presence, expected);
        }
        assert!(
            serde_json::from_str::<BodyControllerState>(r#"{"userPresence":"UNKNOWN"}"#).is_err()
        );
    }

    #[test]
    fn absent_or_invalid_presence_is_not_present() {
        for payload in [
            "{}",
            r#"{"userPresence":null}"#,
            r#"{"userPresence":42}"#,
            r#"{"userPresence":{"unexpected":true}}"#,
            r#"{"userPresence":"VEHICLE_USER_PRESENCE_NOT_PRESENT"}"#,
            r#"{"userPresence":"VEHICLE_USER_PRESENCE_UNKNOWN"}"#,
            r#"{"userPresence":"UNKNOWN"}"#,
            r#"{"userPresence":"VEHICLE_USER_PRESENCE_PRESENT","broken":}"#,
            "null",
            "[]",
        ] {
            assert!(!user_present(payload.as_bytes()), "{payload}");
        }
    }

    #[test]
    fn terminal_state_is_not_lost_in_an_observation_storm() {
        let client = ProtocolClient::new(PathBuf::from("/unused"), PathBuf::from("/unused"));
        for _ in 0..200 {
            emit(&client.inner, "V", 1, "presence_auth_ok", "", Status::Ok);
        }
        emit(
            &client.inner,
            "V",
            1,
            "presence_stopped",
            "bug",
            Status::InternalBug,
        );
        for _ in 0..200 {
            emit(
                &client.inner,
                "V",
                1,
                "presence_error",
                "radio",
                Status::Failed,
            );
        }
        assert_eq!(client.poll_event().unwrap().kind, "presence_stopped");
        assert_eq!(client.inner.events.lock().unwrap().len(), 1);
    }

    #[test]
    fn pairing_rejects_a_stale_public_half_before_loading_go_or_touching_ble() {
        let directory = tempfile::tempdir().unwrap();
        let private = directory.path().join("private_key.pem");
        let public = directory.path().join("public_key.pem");
        crate::keys::generate(&private, &public, false).unwrap();
        let other_private = directory.path().join("other_private.pem");
        let other_public = directory.path().join("other_public.pem");
        crate::keys::generate(&other_private, &other_public, false).unwrap();
        let client = ProtocolClient::new(PathBuf::from("/must-not-load"), directory.path().into());
        let args = vec![
            other_public.to_string_lossy().into_owned(),
            "owner".into(),
            "android_device".into(),
        ];
        let error = client
            .enroll(
                &args,
                "5YJ3E1EA7KF000001",
                private.to_str().unwrap(),
                20,
                5,
                true,
            )
            .err()
            .unwrap();
        assert!(error.to_string().contains("does not match"));
        assert!(client.inner.library.lock().unwrap().is_none());
    }

    #[test]
    #[ignore = "requires the built Go shared library"]
    fn real_library_uses_rust_persisted_keys_and_invalidates_without_children() {
        let path = std::env::var_os("TESLA_BLE_TEST_LIBRARY").expect("TESLA_BLE_TEST_LIBRARY");
        let directory = tempfile::tempdir().unwrap();
        let client = ProtocolClient::new(path.into(), directory.path().into());
        let private = directory.path().join("private_key.pem");
        let first = client.keygen(false, private.to_str().unwrap()).unwrap();
        assert!(first.ok);
        let session = client
            .ensure("5YJ3E1EA7KF000001", private.to_str().unwrap())
            .unwrap();
        let result = execute(&session, Operation::PublicKey, Duration::from_secs(1)).unwrap();
        assert_eq!(result.status, Status::Ok);
        assert_eq!(
            result.payload,
            crate::keys::public_bytes(&directory.path().join("public_key.pem")).unwrap()
        );
        let generation = client.generation();
        client.invalidate();
        assert!(session.stopped());
        assert!(client.generation() > generation);
        assert!(!client.is_alive());
        assert_eq!(
            client
                .keygen(false, private.to_str().unwrap())
                .unwrap()
                .stdout,
            first.stdout
        );
        drop(session);
        drop(client);
    }
}
