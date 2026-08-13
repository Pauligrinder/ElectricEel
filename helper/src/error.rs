/// Error type shared by the in-process control core (`core.rs`) and, when
/// built with the `dbus` feature, the D-Bus daemon surface (`helper.rs`).
///
/// The `#[derive(zbus::DBusError)]` below is feature-gated: the app links the
/// staticlib that builds the plain core, and must not drag in zbus/async-io
/// machinery it never runs. Only the daemon binary enables the `dbus` feature,
/// gaining the D-Bus error name `"org.electriceel.Helper1.<Variant>"` that the
/// pre-Phase-4 QML client matched exactly.
#[derive(Debug)]
#[cfg_attr(
    feature = "dbus",
    derive(zbus::DBusError),
    zbus(prefix = "org.electriceel.Helper1")
)]
pub enum HelperError {
    Forbidden(String),
    Busy(String),
    NotConfigured(String),
    NoKey(String),
    UnknownCommand(String),
    InvalidArgument(String),
    /// Raised instead of the hci fallback when the bluez persistent session
    /// is unusable - falling back to a raw-HCI one-shot would tear down the
    /// very adapter connections (e.g. a soundbar) that bluez mode exists to
    /// preserve, so the failure must surface to the caller instead.
    SessionUnavailable(String),
    #[cfg_attr(feature = "dbus", zbus(error))]
    #[cfg(feature = "dbus")]
    ZBus(zbus::Error),
}

#[cfg(not(feature = "dbus"))]
impl std::fmt::Display for HelperError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            HelperError::Forbidden(m) => write!(f, "forbidden: {m}"),
            HelperError::Busy(m) => write!(f, "busy: {m}"),
            HelperError::NotConfigured(m) => write!(f, "not configured: {m}"),
            HelperError::NoKey(m) => write!(f, "no key: {m}"),
            HelperError::UnknownCommand(m) => write!(f, "unknown command: {m}"),
            HelperError::InvalidArgument(m) => write!(f, "invalid argument: {m}"),
            HelperError::SessionUnavailable(m) => write!(f, "session unavailable: {m}"),
        }
    }
}
