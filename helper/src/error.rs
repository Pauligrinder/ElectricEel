use zbus::DBusError;

/// Custom D-Bus error type for org.teslacontrol.Helper1 methods. Each
/// variant name becomes the D-Bus error name "org.teslacontrol.Helper1.<Variant>"
/// (via the `prefix` attribute below), matching the Go implementation's
/// `dbus.NewError(ifaceName+".Forbidden", ...)` style exactly.
#[derive(Debug, DBusError)]
#[zbus(prefix = "org.teslacontrol.Helper1")]
pub enum HelperError {
    Forbidden(String),
    Busy(String),
    NotConfigured(String),
    NoKey(String),
    UnknownCommand(String),
    InvalidArgument(String),
    #[zbus(error)]
    ZBus(zbus::Error),
}
