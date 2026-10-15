//! Safe owner of the in-process Go protocol library (ABI v2, see
//! `helper/session/abi/tesla_ble.h`), driven by `protocol_client.rs`.
//!
//! Submission is non-blocking; completion callbacks copy into Rust channels.
//! Call `shutdown().await` on a backend worker before dropping a session. Drop
//! is a blocking safety backstop, never an excuse to destroy live callback data.
//! Successfully loaded Go libraries remain loaded for the process lifetime.

use std::collections::HashMap;
use std::ffi::{CStr, CString};
use std::os::unix::ffi::OsStrExt;
use std::path::Path;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

const ABI_VERSION: u32 = 2;
const MAX_CALLBACK_BYTES: usize = 1024 * 1024;

#[repr(C)]
#[derive(Clone, Copy)]
struct Bytes {
    data: *const u8,
    len: u64,
}

impl Bytes {
    fn borrowed(bytes: &[u8]) -> Self {
        Self {
            data: bytes.as_ptr(),
            len: bytes.len() as u64,
        }
    }
}

#[repr(C)]
struct Config {
    size: u32,
    version: u32,
    vin: Bytes,
    adapter: Bytes,
    private_scalar: Bytes,
}

#[repr(C)]
struct Request {
    size: u32,
    operation: u32,
    id: u64,
    timeout_ms: u64,
    domain: u32,
    reserved: u32,
    latitude: f64,
    longitude: f64,
    text: Bytes,
    target: u64,
    command: u32,
    count: u32,
    values: *const RawValue,
}

#[repr(C)]
struct RawValue {
    kind: u32,
    reserved: u32,
    integer: u64,
    number: f64,
    bytes: Bytes,
}

#[derive(Debug, Clone)]
pub enum Value {
    Integer(u64),
    Number(f64),
    Bytes(Vec<u8>),
}

/// Typed command parameter tags (`ee_value.kind`); 0 means omitted.
#[derive(Clone, Copy)]
#[repr(u32)]
enum ValueKind {
    Integer = 1,
    Number = 2,
    Bytes = 3,
}

/// Callback message kinds (`enum ee_notification`).
#[derive(Clone, Copy, Debug, PartialEq, Eq, strum::FromRepr)]
#[repr(u32)]
enum CallbackKind {
    Complete = 1,
    Diagnostic = 2,
    Stopped = 3,
    Observation = 4,
}

/// Observation kinds (`enum ee_observation`).
#[derive(Clone, Copy, Debug, PartialEq, Eq, strum::FromRepr)]
#[repr(u32)]
pub enum ObservationKind {
    Advertisement = 1,
    Disconnected = 2,
    Authenticated = 3,
    AuthFailed = 4,
    WatchFailed = 5,
    Log = 6,
}

#[derive(Debug, Clone)]
pub struct Observation {
    pub kind: ObservationKind,
    pub target: u64,
    pub rssi: i32,
    pub value: u32,
    pub status: Status,
    pub error: String,
}

#[repr(C)]
struct Message {
    size: u32,
    kind: u32,
    session: u64,
    request: u64,
    status: u32,
    reserved: u32,
    payload: Bytes,
    error: Bytes,
    event: u32,
    rssi: i32,
    target: u64,
    value: u32,
    padding: u32,
}

type Callback = unsafe extern "C" fn(*mut libc::c_void, *const Message);
type Create = unsafe extern "C" fn(*mut Config, Callback, *mut libc::c_void, *mut u64) -> u32;
type Submit = unsafe extern "C" fn(u64, *mut Request) -> u32;
type Cancel = unsafe extern "C" fn(u64, u64) -> u32;
type Lifecycle = unsafe extern "C" fn(u64) -> u32;

/// Values mirror `helper/session/abi/tesla_ble.h`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status {
    Ok,
    Invalid,
    Busy,
    Stopping,
    InvalidHandle,
    Failed,
    Cancelled,
    TimedOut,
    InternalBug,
    Uncertain,
    NotConnected,
    BluetoothOff,
    AuthenticationFailed,
    Unknown(u32),
}

impl From<u32> for Status {
    fn from(value: u32) -> Self {
        match value {
            0 => Self::Ok,
            1 => Self::Invalid,
            2 => Self::Busy,
            3 => Self::Stopping,
            4 => Self::InvalidHandle,
            5 => Self::Failed,
            6 => Self::Cancelled,
            7 => Self::TimedOut,
            8 => Self::InternalBug,
            9 => Self::Uncertain,
            10 => Self::NotConnected,
            11 => Self::BluetoothOff,
            12 => Self::AuthenticationFailed,
            other => Self::Unknown(other),
        }
    }
}

#[derive(Clone, Copy, Debug)]
pub enum Domain {
    Unauthenticated,
    Vcsec,
    Infotainment,
}

#[derive(Debug)]
pub enum Operation {
    PublicKey,
    Connect(Domain),
    Disconnect,
    BodyControllerState,
    NavigateGps { latitude: f64, longitude: f64 },
    NavigateAddress(String),
    ConnectTarget { domain: Domain, target: u64 },
    WatchStart,
    WatchStop,
    PassiveEntry(bool),
    EnrollmentStatus(Vec<u8>),
    Command { command: u32, values: Vec<Value> },
}

#[derive(Debug)]
pub struct Outcome {
    pub status: Status,
    pub payload: Vec<u8>,
    pub error: String,
}

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("cannot load Tesla BLE library: {0}")]
    Load(String),
    #[error("incompatible Tesla BLE ABI version: {0}")]
    Version(u32),
    #[error("Tesla BLE submission/lifecycle rejected: {0:?}")]
    Rejected(Status),
    #[error("Tesla BLE callback channel closed")]
    Closed,
}

pub struct Library {
    create: Create,
    submit: Submit,
    cancel: Cancel,
    stop: Lifecycle,
    destroy: Lifecycle,
}

impl Library {
    /// Load trusted application code, never a user-supplied library path.
    /// The ABI is checked before any session can be constructed.
    ///
    /// # Errors
    /// Returns an error for loader/symbol failures or an incompatible ABI.
    ///
    /// # Safety
    /// The path must identify trusted application code implementing the exact
    /// ABI in `tesla_ble.h`. A version number alone cannot validate signatures.
    pub unsafe fn load(path: &Path) -> Result<Arc<Self>, Error> {
        let path = CString::new(path.as_os_str().as_bytes())
            .map_err(|error| Error::Load(error.to_string()))?;
        // SAFETY: NUL-terminated path; the handle is either null or a dlopen handle.
        let handle = unsafe { libc::dlopen(path.as_ptr(), libc::RTLD_NOW | libc::RTLD_LOCAL) };
        if handle.is_null() {
            return Err(Error::Load(dl_error()));
        }
        // Do not dlclose a Go shared library, including on symbol/version errors:
        // its runtime may already have started threads in library constructors.
        let version: unsafe extern "C" fn() -> u32 =
            unsafe { std::mem::transmute(symbol(handle, c"ee_abi_version")?) };
        // SAFETY: the version function has no arguments and ABI v1 returns u32.
        let version = unsafe { version() };
        if version != ABI_VERSION {
            return Err(Error::Version(version));
        }
        // SAFETY: trusted ABI-v1 library exports signatures from tesla_ble.h.
        unsafe {
            Ok(Arc::new(Self {
                create: std::mem::transmute::<*mut libc::c_void, Create>(symbol(
                    handle,
                    c"ee_session_create",
                )?),
                submit: std::mem::transmute::<*mut libc::c_void, Submit>(symbol(
                    handle,
                    c"ee_session_submit",
                )?),
                cancel: std::mem::transmute::<*mut libc::c_void, Cancel>(symbol(
                    handle,
                    c"ee_session_cancel",
                )?),
                stop: std::mem::transmute::<*mut libc::c_void, Lifecycle>(symbol(
                    handle,
                    c"ee_session_stop",
                )?),
                destroy: std::mem::transmute::<*mut libc::c_void, Lifecycle>(symbol(
                    handle,
                    c"ee_session_destroy",
                )?),
            }))
        }
    }
}

fn dl_error() -> String {
    // SAFETY: dlerror returns a thread-local NUL-terminated string or null.
    let error = unsafe { libc::dlerror() };
    if error.is_null() {
        "unknown dynamic loader error".into()
    } else {
        unsafe { CStr::from_ptr(error) }
            .to_string_lossy()
            .into_owned()
    }
}

unsafe fn symbol(handle: *mut libc::c_void, name: &CStr) -> Result<*mut libc::c_void, Error> {
    // SAFETY: valid live dlopen handle and NUL-terminated symbol name.
    let address = unsafe { libc::dlsym(handle, name.as_ptr()) };
    if address.is_null() {
        Err(Error::Load(dl_error()))
    } else {
        Ok(address)
    }
}

struct CallbackState {
    handle: AtomicU64,
    pending: Mutex<HashMap<u64, async_channel::Sender<Outcome>>>,
    stopped: async_channel::Sender<()>,
    diagnostics: async_channel::Sender<String>,
    lost_diagnostics: AtomicU64,
    faulted: AtomicBool,
    events: Mutex<std::collections::VecDeque<Observation>>,
    event_ready: async_channel::Sender<()>,
}

impl CallbackState {
    fn fail(&self, reason: &str) {
        self.faulted.store(true, Ordering::SeqCst);
        let pending = std::mem::take(
            &mut *self
                .pending
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner),
        );
        for (_, sender) in pending {
            let _ = sender.try_send(Outcome {
                status: Status::InternalBug,
                payload: Vec::new(),
                error: reason.into(),
            });
        }
        self.diagnose(reason.into());
    }
}

impl CallbackState {
    fn diagnose(&self, diagnostic: String) {
        if self.diagnostics.try_send(diagnostic).is_err() {
            self.lost_diagnostics.fetch_add(1, Ordering::Relaxed);
        }
    }

    fn observe(&self, message: &Message) {
        let Some(kind) = ObservationKind::from_repr(message.event) else {
            self.diagnose(format!(
                "ignoring unknown ABI observation kind {}",
                message.event
            ));
            return;
        };
        let error = unsafe { copy_bytes(message.error) }.unwrap_or_default();
        let event = Observation {
            kind,
            target: message.target,
            rssi: message.rssi,
            value: message.value,
            status: message.status.into(),
            error: String::from_utf8_lossy(&error).into_owned(),
        };
        if event.kind == ObservationKind::Log {
            self.diagnose(event.error);
            return;
        }
        if event.status == Status::InternalBug {
            self.faulted.store(true, Ordering::SeqCst);
        }
        let mut events = self
            .events
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        // Coalesce advertisement snapshots, never silently discard a
        // terminal/error observation. Overflow faults the session.
        if event.kind == ObservationKind::Advertisement {
            events.retain(|event| event.kind != ObservationKind::Advertisement);
        }
        if events.len() == 64 {
            self.fail("protocol observation queue overflow");
        } else {
            events.push_back(event);
        }
        let _ = self.event_ready.try_send(());
    }
}

unsafe fn copy_bytes(bytes: Bytes) -> Option<Vec<u8>> {
    let length = usize::try_from(bytes.len).ok()?;
    if length > MAX_CALLBACK_BYTES || (length != 0 && bytes.data.is_null()) {
        return None;
    }
    if length == 0 {
        return Some(Vec::new());
    }
    // SAFETY: the trusted library provides readable storage for this callback.
    Some(unsafe { std::slice::from_raw_parts(bytes.data, length) }.to_vec())
}

unsafe extern "C" fn callback(context: *mut libc::c_void, message: *const Message) {
    // Never unwind into Go/C. The context remains owned until destroy's barrier.
    let delivery = std::panic::catch_unwind(|| {
        if context.is_null() || message.is_null() {
            return;
        }
        // SAFETY: library callback contract and Session's boxed state lifetime.
        let state = unsafe { &*context.cast::<CallbackState>() };
        let message = unsafe { &*message };
        if message.size as usize != std::mem::size_of::<Message>()
            || message.reserved != 0
            || message.session != state.handle.load(Ordering::SeqCst)
        {
            state.fail("invalid ABI callback envelope/session");
            return;
        }
        let Some(kind) = CallbackKind::from_repr(message.kind) else {
            state.fail("unknown ABI callback kind");
            return;
        };
        match kind {
            CallbackKind::Complete => {
                let payload = unsafe { copy_bytes(message.payload) };
                let error = unsafe { copy_bytes(message.error) };
                let outcome = match (payload, error) {
                    (Some(payload), Some(error)) => Outcome {
                        status: message.status.into(),
                        payload,
                        error: String::from_utf8_lossy(&error).into_owned(),
                    },
                    _ => Outcome {
                        status: Status::InternalBug,
                        payload: Vec::new(),
                        error: "invalid callback buffer".into(),
                    },
                };
                if outcome.status == Status::InternalBug {
                    state.faulted.store(true, Ordering::SeqCst);
                }
                let sender = state
                    .pending
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner)
                    .remove(&message.request);
                if let Some(sender) = sender {
                    let _ = sender.try_send(outcome); // Capacity one, exactly one completion.
                } else {
                    state.fail("duplicate or unknown operation completion");
                }
            }
            CallbackKind::Diagnostic => {
                state.faulted.store(true, Ordering::SeqCst);
                if let Some(error) = unsafe { copy_bytes(message.error) } {
                    state.diagnose(String::from_utf8_lossy(&error).into_owned());
                }
            }
            CallbackKind::Stopped => {
                let _ = state.stopped.try_send(());
                state.stopped.close();
                state.diagnostics.close();
                state.event_ready.close();
            }
            CallbackKind::Observation => state.observe(message),
        }
    });
    if delivery.is_err() && !context.is_null() {
        // SAFETY: this is the same still-live callback context used above.
        unsafe { &*context.cast::<CallbackState>() }.fail("Rust ABI callback panicked");
    }
}

pub struct Session {
    library: Arc<Library>,
    handle: u64,
    state: std::mem::ManuallyDrop<Box<CallbackState>>,
    stopped: async_channel::Receiver<()>,
    diagnostics: async_channel::Receiver<String>,
    next_id: AtomicU64,
    admission: Mutex<()>,
    event_wait: async_channel::Receiver<()>,
}

impl Session {
    /// Construct a protocol session without touching Bluetooth or the filesystem.
    ///
    /// # Errors
    /// Returns a rejection if the VIN, key or ABI configuration is invalid.
    pub fn new(
        library: Arc<Library>,
        vin: &str,
        adapter: &str,
        private_scalar: &[u8; 32],
    ) -> Result<Self, Error> {
        let (stop_tx, stopped) = async_channel::bounded(1);
        let (log_tx, diagnostics) = async_channel::bounded(64);
        let (event_ready, event_wait) = async_channel::bounded(1);
        let mut state = Box::new(CallbackState {
            handle: AtomicU64::new(0),
            pending: Mutex::new(HashMap::new()),
            stopped: stop_tx,
            diagnostics: log_tx,
            lost_diagnostics: AtomicU64::new(0),
            faulted: AtomicBool::new(false),
            events: Mutex::new(std::collections::VecDeque::new()),
            event_ready,
        });
        let mut config = Config {
            size: u32::try_from(std::mem::size_of::<Config>())
                .map_err(|_| Error::Rejected(Status::Invalid))?,
            version: ABI_VERSION,
            vin: Bytes::borrowed(vin.as_bytes()),
            adapter: Bytes::borrowed(adapter.as_bytes()),
            private_scalar: Bytes::borrowed(private_scalar),
        };
        let mut handle = 0;
        // SAFETY: borrowed config lives through create; boxed state remains stable
        // until destroy; Go copies all configuration/key bytes before returning.
        let status = unsafe {
            (library.create)(
                &raw mut config,
                callback,
                (&raw mut *state).cast(),
                &raw mut handle,
            )
        };
        if status != 0 {
            return Err(Error::Rejected(status.into()));
        }
        state.handle.store(handle, Ordering::SeqCst);
        Ok(Self {
            library,
            handle,
            state: std::mem::ManuallyDrop::new(state),
            stopped,
            diagnostics,
            next_id: AtomicU64::new(1),
            admission: Mutex::new(()),
            event_wait,
        })
    }

    #[allow(clippy::too_many_lines)] // Explicit ABI layouts and operation mapping.
    fn submit(
        &self,
        operation: &Operation,
        timeout: Duration,
    ) -> Result<(u64, async_channel::Receiver<Outcome>), Error> {
        let _admission = self
            .admission
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if self.state.faulted.load(Ordering::SeqCst) {
            return Err(Error::Rejected(Status::InternalBug));
        }
        // The admission mutex serializes ID allocation and submission order.
        let id = self.next_id.load(Ordering::Relaxed);
        let next = id.checked_add(1).ok_or(Error::Rejected(Status::Invalid))?;
        self.next_id.store(next, Ordering::Relaxed);
        let mut request = Request {
            size: u32::try_from(std::mem::size_of::<Request>())
                .map_err(|_| Error::Rejected(Status::Invalid))?,
            operation: 0,
            id,
            timeout_ms: u64::try_from(timeout.as_millis())
                .map_err(|_| Error::Rejected(Status::Invalid))?,
            domain: 0,
            reserved: 0,
            latitude: 0.0,
            longitude: 0.0,
            text: Bytes::borrowed(&[]),
            target: 0,
            command: 0,
            count: 0,
            values: std::ptr::null(),
        };
        let raw_values: Vec<RawValue> = match operation {
            Operation::Command { values, .. } => values
                .iter()
                .map(|value| match value {
                    Value::Integer(integer) => RawValue {
                        kind: ValueKind::Integer as u32,
                        reserved: 0,
                        integer: *integer,
                        number: 0.0,
                        bytes: Bytes::borrowed(&[]),
                    },
                    Value::Number(number) => RawValue {
                        kind: ValueKind::Number as u32,
                        reserved: 0,
                        integer: 0,
                        number: *number,
                        bytes: Bytes::borrowed(&[]),
                    },
                    Value::Bytes(bytes) => RawValue {
                        kind: ValueKind::Bytes as u32,
                        reserved: 0,
                        integer: 0,
                        number: 0.0,
                        bytes: Bytes::borrowed(bytes),
                    },
                })
                .collect(),
            _ => Vec::new(),
        };
        match operation {
            Operation::PublicKey => request.operation = 11,
            Operation::Connect(domain) | Operation::ConnectTarget { domain, .. } => {
                request.operation = 1;
                request.domain = match domain {
                    Domain::Unauthenticated => 0,
                    Domain::Vcsec => 1,
                    Domain::Infotainment => 2,
                };
                if let Operation::ConnectTarget { target, .. } = operation {
                    request.target = *target;
                }
            }
            Operation::Disconnect => request.operation = 2,
            Operation::BodyControllerState => request.operation = 3,
            Operation::NavigateGps {
                latitude,
                longitude,
            } => {
                request.operation = 4;
                request.latitude = *latitude;
                request.longitude = *longitude;
            }
            Operation::NavigateAddress(text) => {
                request.operation = 5;
                request.text = Bytes::borrowed(text.as_bytes());
            }
            Operation::WatchStart => request.operation = 6,
            Operation::WatchStop => request.operation = 7,
            Operation::PassiveEntry(enabled) => {
                request.operation = 8;
                request.domain = u32::from(*enabled);
            }
            Operation::EnrollmentStatus(key) => {
                request.operation = 9;
                request.text = Bytes::borrowed(key);
            }
            Operation::Command { command, .. } => {
                request.operation = 10;
                request.command = *command;
                request.count = u32::try_from(raw_values.len())
                    .map_err(|_| Error::Rejected(Status::Invalid))?;
                request.values = raw_values.as_ptr();
            }
        }
        let (sender, receiver) = async_channel::bounded(1);
        self.state
            .pending
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .insert(id, sender);
        // SAFETY: live handle; request/buffers remain valid until Go has copied them.
        let status = unsafe { (self.library.submit)(self.handle, &raw mut request) };
        if status != 0 {
            self.state
                .pending
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .remove(&id);
            return Err(Error::Rejected(status.into()));
        }
        Ok((id, receiver))
    }

    /// Submit an operation and await its completion. Dropping this future requests
    /// cancellation; it never implies that a transmitted vehicle action was undone.
    ///
    /// # Errors
    /// Returns admission errors (including busy/stopping) or a closed callback channel.
    pub async fn execute(&self, operation: Operation, timeout: Duration) -> Result<Outcome, Error> {
        let (id, receiver) = self.submit(&operation, timeout)?;
        let mut cancellation = CancelOnDrop {
            session: self,
            id,
            armed: true,
        };
        let result = receiver.recv().await.map_err(|_| Error::Closed);
        cancellation.armed = false;
        result
    }

    pub fn stop(&self) {
        // SAFETY: live session; stop is non-blocking and idempotent.
        unsafe { (self.library.stop)(self.handle) };
    }

    pub fn cancel_operations(&self) {
        let ids: Vec<u64> = self
            .state
            .pending
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .keys()
            .copied()
            .collect();
        for id in ids {
            // SAFETY: valid live handle; missing/already-completed IDs are rejected.
            unsafe { (self.library.cancel)(self.handle, id) };
        }
    }

    /// Invoke on a backend worker. Drop afterwards performs the final callback
    /// return barrier; receiving stopped alone does not authorize freeing context.
    pub async fn shutdown(&self) {
        self.stop();
        let _ = self.stopped.recv().await;
    }

    /// Receive a panic diagnostic without doing file I/O on a Go callback thread.
    ///
    /// # Errors
    /// Returns an error if the diagnostic channel has closed.
    pub async fn diagnostic(&self) -> Result<String, Error> {
        self.diagnostics.recv().await.map_err(|_| Error::Closed)
    }

    pub fn lost_diagnostics(&self) -> u64 {
        self.state.lost_diagnostics.load(Ordering::Relaxed)
    }

    pub fn poll_observation(&self) -> Option<Observation> {
        self.state
            .events
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .pop_front()
    }

    pub async fn wait_observation(&self) {
        let _ = self.event_wait.recv().await;
    }

    pub fn poll_diagnostic(&self) -> Option<String> {
        self.diagnostics.try_recv().ok()
    }

    pub fn faulted(&self) -> bool {
        self.state.faulted.load(Ordering::SeqCst)
    }

    pub fn stopped(&self) -> bool {
        self.stopped.is_closed()
    }
}

struct CancelOnDrop<'a> {
    session: &'a Session,
    id: u64,
    armed: bool,
}

impl Drop for CancelOnDrop<'_> {
    fn drop(&mut self) {
        if self.armed {
            // SAFETY: the borrowed session outlives this guard.
            unsafe { (self.session.library.cancel)(self.session.handle, self.id) };
        }
    }
}

impl Drop for Session {
    fn drop(&mut self) {
        self.stop();
        // SAFETY: stop precedes destroy, no methods can race Rust destruction,
        // and the callback box is retained until Go returns from its barrier.
        let status = unsafe { (self.library.destroy)(self.handle) };
        if status == 0 {
            // SAFETY: Go's successful destroy proves no callback retains it.
            unsafe { std::mem::ManuallyDrop::drop(&mut self.state) };
        } else {
            // An ABI bug cannot authorize freeing memory still owned by Go.
            // Keep the box alive for diagnosis rather than risk use-after-free.
            eprintln!(
                "Tesla BLE destroy failed: {:?}; retaining callback context",
                Status::from(status)
            );
        }
    }
}

/// Test-only, in-process stand-in for the Go ABI (`tesla_ble.h`), so the
/// production `ProtocolClient`/`Core`/runtime path can be exercised without
/// the Go shared library. Sessions are scripted by VIN: every operation
/// completes synchronously with `Ok` unless the VIN's script overrides it.
#[cfg(test)]
pub(crate) mod fake {
    pub(crate) use super::ObservationKind;
    use super::{Arc, Bytes, Callback, CallbackKind, Config, Library, Message, Request};
    use std::collections::HashMap;
    use std::sync::atomic::{AtomicU64, Ordering};
    use std::sync::{LazyLock, Mutex};
    use std::time::Duration;

    /// Operation codes from `tesla_ble.h`.
    pub(crate) const WATCH_START: u32 = 6;
    pub(crate) const WATCH_STOP: u32 = 7;
    pub(crate) const VEHICLE_COMMAND: u32 = 10;
    /// Status codes.
    pub(crate) const FAILED: u32 = 5;
    pub(crate) const CANCELLED: u32 = 6;
    pub(crate) const NOT_CONNECTED: u32 = 10;
    pub(crate) const BLUETOOTH_OFF: u32 = 11;

    #[derive(Clone, Default)]
    pub(crate) struct Script {
        /// Completion (status, error) per operation code; default is `Ok`.
        pub(crate) statuses: HashMap<u32, (u32, String)>,
        /// Operations that complete only when cancelled or stopped.
        pub(crate) hold: Vec<u32>,
        /// Time `ee_session_destroy` spends in backend teardown.
        pub(crate) destroy_delay: Duration,
    }

    struct Live {
        callback: Callback,
        context: usize,
        vin: String,
        stopped: bool,
        held: Vec<u64>,
    }

    static SCRIPTS: LazyLock<Mutex<HashMap<String, Script>>> =
        LazyLock::new(|| Mutex::new(HashMap::new()));
    static LIVE: LazyLock<Mutex<HashMap<u64, Live>>> = LazyLock::new(|| Mutex::new(HashMap::new()));
    /// Every submitted operation code, per VIN.
    static SUBMITTED: LazyLock<Mutex<HashMap<String, Vec<u32>>>> =
        LazyLock::new(|| Mutex::new(HashMap::new()));
    static NEXT: AtomicU64 = AtomicU64::new(1);

    pub(crate) fn script(vin: &str, script: Script) {
        SCRIPTS.lock().unwrap().insert(vin.into(), script);
    }

    /// Operation codes submitted so far for this VIN.
    pub(crate) fn submitted(vin: &str) -> Vec<u32> {
        SUBMITTED
            .lock()
            .unwrap()
            .get(vin)
            .cloned()
            .unwrap_or_default()
    }

    /// Number of held (not yet completed) operations for this VIN.
    pub(crate) fn held(vin: &str) -> usize {
        LIVE.lock()
            .unwrap()
            .values()
            .filter(|live| live.vin == vin)
            .map(|live| live.held.len())
            .sum()
    }

    pub(crate) fn library() -> Arc<Library> {
        Arc::new(Library {
            create,
            submit,
            cancel,
            stop,
            destroy,
        })
    }

    fn message(kind: CallbackKind, session: u64) -> Message {
        Message {
            size: u32::try_from(std::mem::size_of::<Message>()).unwrap(),
            kind: kind as u32,
            session,
            request: 0,
            status: 0,
            reserved: 0,
            payload: Bytes::borrowed(&[]),
            error: Bytes::borrowed(&[]),
            event: 0,
            rssi: 0,
            target: 0,
            value: 0,
            padding: 0,
        }
    }

    fn deliver(handle: u64, message: &Message) {
        let target = LIVE
            .lock()
            .unwrap()
            .get(&handle)
            .map(|live| (live.callback, live.context));
        if let Some((callback, context)) = target {
            // SAFETY: the context stays live until destroy removes the handle.
            unsafe { callback(context as *mut libc::c_void, message) };
        }
    }

    fn complete(handle: u64, request: u64, status: u32, error: &str) {
        let mut completion = message(CallbackKind::Complete, handle);
        completion.request = request;
        completion.status = status;
        completion.error = Bytes::borrowed(error.as_bytes());
        deliver(handle, &completion);
    }

    /// Deliver one raw observation to every live session of this VIN.
    pub(crate) fn observe(vin: &str, event: ObservationKind, status: u32) {
        observe_target(vin, event, status, 0, 0);
    }

    /// Deliver an observation carrying a discovery target and RSSI.
    pub(crate) fn observe_target(
        vin: &str,
        event: ObservationKind,
        status: u32,
        target: u64,
        rssi: i32,
    ) {
        let handles: Vec<u64> = LIVE
            .lock()
            .unwrap()
            .iter()
            .filter(|(_, live)| live.vin == vin && !live.stopped)
            .map(|(handle, _)| *handle)
            .collect();
        for handle in handles {
            let mut observation = message(CallbackKind::Observation, handle);
            observation.event = event as u32;
            observation.status = status;
            observation.target = target;
            observation.rssi = rssi;
            deliver(handle, &observation);
        }
    }

    unsafe extern "C" fn create(
        config: *mut Config,
        callback: Callback,
        context: *mut libc::c_void,
        handle: *mut u64,
    ) -> u32 {
        // SAFETY: Rust's Session::new passes a live config and handle slot.
        let config = unsafe { &*config };
        let length = usize::try_from(config.vin.len).unwrap();
        // SAFETY: the VIN bytes are borrowed for the duration of create.
        let vin = unsafe { std::slice::from_raw_parts(config.vin.data, length) };
        let id = NEXT.fetch_add(1, Ordering::SeqCst);
        LIVE.lock().unwrap().insert(
            id,
            Live {
                callback,
                context: context as usize,
                vin: String::from_utf8_lossy(vin).into_owned(),
                stopped: false,
                held: Vec::new(),
            },
        );
        // SAFETY: caller-owned output slot.
        unsafe { *handle = id };
        0
    }

    unsafe extern "C" fn submit(handle: u64, request: *mut Request) -> u32 {
        // SAFETY: Rust's Session::submit passes a live request.
        let request = unsafe { &*request };
        let vin = match LIVE.lock().unwrap().get(&handle) {
            Some(live) if !live.stopped => live.vin.clone(),
            Some(_) => return 3,
            None => return 4,
        };
        SUBMITTED
            .lock()
            .unwrap()
            .entry(vin.clone())
            .or_default()
            .push(request.operation);
        let script = SCRIPTS
            .lock()
            .unwrap()
            .get(&vin)
            .cloned()
            .unwrap_or_default();
        if script.hold.contains(&request.operation) {
            if let Some(live) = LIVE.lock().unwrap().get_mut(&handle) {
                live.held.push(request.id);
            }
            return 0;
        }
        let (status, error) = script
            .statuses
            .get(&request.operation)
            .cloned()
            .unwrap_or_default();
        complete(handle, request.id, status, &error);
        0
    }

    unsafe extern "C" fn cancel(handle: u64, request: u64) -> u32 {
        let held = LIVE.lock().unwrap().get_mut(&handle).is_some_and(|live| {
            let before = live.held.len();
            live.held.retain(|id| *id != request);
            live.held.len() != before
        });
        if held {
            complete(handle, request, CANCELLED, "cancelled");
        }
        0
    }

    unsafe extern "C" fn stop(handle: u64) -> u32 {
        let held = LIVE.lock().unwrap().get_mut(&handle).and_then(|live| {
            (!std::mem::replace(&mut live.stopped, true)).then(|| std::mem::take(&mut live.held))
        });
        if let Some(held) = held {
            for request in held {
                complete(handle, request, CANCELLED, "session stopped");
            }
            deliver(handle, &message(CallbackKind::Stopped, handle));
        }
        0
    }

    unsafe extern "C" fn destroy(handle: u64) -> u32 {
        let vin = LIVE
            .lock()
            .unwrap()
            .get(&handle)
            .map(|live| live.vin.clone());
        let delay = vin
            .and_then(|vin| SCRIPTS.lock().unwrap().get(&vin).map(|s| s.destroy_delay))
            .unwrap_or_default();
        std::thread::sleep(delay);
        LIVE.lock().unwrap().remove(&handle);
        0
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use p256::elliptic_curve::sec1::ToEncodedPoint;

    #[test]
    fn missing_library_is_an_explicit_error() {
        assert!(matches!(
            unsafe { Library::load(Path::new("/nonexistent/libteslable.so")) },
            Err(Error::Load(_))
        ));
    }

    // Run explicitly with TESLA_BLE_TEST_LIBRARY pointing at the actual Go .so.
    #[test]
    #[ignore = "requires the built Go shared library"]
    fn real_go_library_copies_keys_delivers_results_and_stops() {
        let path = std::env::var_os("TESLA_BLE_TEST_LIBRARY").expect("TESLA_BLE_TEST_LIBRARY");
        // SAFETY: test path names the Go library built from this repository.
        let library = unsafe { Library::load(Path::new(&path)) }.unwrap();
        let mut scalar = [0; 32];
        scalar[31] = 1;
        assert!(matches!(
            Session::new(Arc::clone(&library), "invalid", "", &scalar),
            Err(Error::Rejected(Status::Invalid))
        ));
        let session = Session::new(library, "5YJ3E1EA7KF000001", "", &scalar).unwrap();
        scalar.fill(0);
        futures_lite::future::block_on(async {
            let key = session
                .execute(Operation::PublicKey, Duration::from_secs(1))
                .await
                .unwrap();
            let mut original = [0; 32];
            original[31] = 1;
            assert_eq!(
                key.payload,
                p256::SecretKey::from_slice(&original)
                    .unwrap()
                    .public_key()
                    .to_encoded_point(false)
                    .as_bytes()
            );
            let result = session
                .execute(Operation::Disconnect, Duration::from_secs(1))
                .await
                .unwrap();
            assert_eq!(result.status, Status::Ok);
            let result = session
                .execute(Operation::BodyControllerState, Duration::from_secs(1))
                .await
                .unwrap();
            assert_eq!(result.status, Status::NotConnected);
            let result = session
                .execute(
                    Operation::NavigateGps {
                        latitude: 100.0,
                        longitude: 0.0,
                    },
                    Duration::from_secs(1),
                )
                .await
                .unwrap();
            assert_eq!(result.status, Status::Invalid);
            session.shutdown().await;
            assert!(matches!(
                session
                    .execute(Operation::Disconnect, Duration::from_secs(1))
                    .await,
                Err(Error::Rejected(Status::Stopping))
            ));
        });
        drop(session); // Exercises Go's callback return barrier before Box release.
    }
}
