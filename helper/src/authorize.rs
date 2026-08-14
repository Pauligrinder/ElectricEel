use std::fs;

use zbus::names::BusName;

use crate::error::HelperError;

/// `allowed_callers` is the set of caller binary paths (resolved via
/// `/proc/<pid>/exe`, see authorize) permitted to invoke the privileged
/// methods below. This exists because the D-Bus system policy
/// (org.electriceel.Helper.conf) can only scope access to the
/// "defaultuser" Unix account - Sailfish's single-user model - not to a
/// specific application, so *any* process running as defaultuser could
/// otherwise call these methods directly, bypassing the Sailjail
/// permission that's meant to gate harbour-electric-eel specifically.
///
/// Confirmed on-device (Jolla Phone 2026, Sailfish 5.2.0.16): Sailjail
/// routes a sandboxed app's *entire* system-bus traffic through a
/// dedicated per-app xdg-dbus-proxy process, so the resolved caller PID/exe
/// for a legitimate sandboxed call is that proxy's (/usr/bin/xdg-dbus-proxy),
/// never harbour-electric-eel's own PID - confirmed via `busctl list
/// --system` showing the app's connections owned by the proxy PID, and via
/// `firejail --debug` showing the proxy's filter args correctly include
/// org.electriceel.Helper. So the actual per-app gate is one layer up:
/// only an app whose .desktop file declares the `ElectricEelHelper`
/// permission gets this bus name added to its own proxy's filter at all.
/// This allow-list's job is narrower than originally intended - it can't
/// distinguish harbour-electric-eel from any other `ElectricEelHelper`-
/// permitted app, but it still blocks a rogue *unsandboxed* process running
/// directly as defaultuser (which connects to the bus directly, not through
/// a proxy, so its own exe won't match either entry below).
///
/// Resolving the proxy's own exe also requires `CAP_SYS_PTRACE`, since
/// the daemon runs as its own unprivileged "electric-eel" user, and
/// reading another UID's `/proc/<pid>/exe` symlink is denied by the kernel
/// without it, regardless of Yama `ptrace_scope` - see the old
/// systemd unit's `AmbientCapabilities` (deleted in Phase 4).
#[must_use]
pub fn default_allowed_callers() -> Vec<String> {
    let raw = std::env::var("ELECTRICEEL_ALLOWED_CALLERS")
        .unwrap_or_else(|_| "/usr/bin/harbour-electric-eel,/usr/bin/xdg-dbus-proxy".to_string());
    resolve_caller_paths(&split_and_trim(&raw))
}

pub(crate) fn split_and_trim(s: &str) -> Vec<String> {
    s.split(',')
        .map(str::trim)
        .filter(|p| !p.is_empty())
        .map(str::to_string)
        .collect()
}

pub(crate) fn resolve_caller_paths(paths: &[String]) -> Vec<String> {
    paths
        .iter()
        .map(|p| match fs::canonicalize(p) {
            Ok(resolved) => resolved.to_string_lossy().into_owned(),
            Err(_) => p.clone(),
        })
        .collect()
}

/// Resolves the D-Bus caller's PID/UID and binary path and checks it against
/// `allowed_callers`, denying by default on any lookup failure.
///
/// The PID and UID come from the D-Bus daemon atomically in one
/// `GetConnectionCredentials` reply. The PID is then cross-checked against the
/// still-running process's real UID read from /proc: if the caller exited
/// and its PID was recycled between the credentials reply and the /proc
/// read, the two UIDs will disagree and the call is denied. This closes the
/// PID-reuse TOCTOU without comparing against an expected UID (the caller
/// legitimately runs as the Sailfish user, not as the daemon's own service
/// account).
pub(crate) fn authorize(
    conn: &zbus::blocking::Connection,
    sender: &str,
    allowed_callers: &[String],
) -> Result<(), HelperError> {
    let bus_name = BusName::try_from(sender)
        .map_err(|_| HelperError::Forbidden("cannot resolve caller credentials".to_string()))?;
    let dbus_proxy = zbus::blocking::fdo::DBusProxy::new(conn)
        .map_err(|_| HelperError::Forbidden("cannot resolve caller credentials".to_string()))?;
    let creds = dbus_proxy
        .get_connection_credentials(bus_name)
        .map_err(|_| HelperError::Forbidden("cannot resolve caller credentials".to_string()))?;

    let pid = creds
        .process_id()
        .ok_or_else(|| HelperError::Forbidden("caller credentials incomplete".to_string()))?;
    let cred_uid = creds
        .unix_user_id()
        .ok_or_else(|| HelperError::Forbidden("caller credentials incomplete".to_string()))?;

    let proc = procfs::process::Process::new(pid.cast_signed())
        .map_err(|_| HelperError::Forbidden("cannot resolve caller uid".to_string()))?;
    let proc_uid = proc
        .uid()
        .map_err(|_| HelperError::Forbidden("cannot resolve caller uid".to_string()))?;
    if proc_uid != cred_uid {
        eprintln!(
            "electric-eel: rejected call from pid {pid}: uid mismatch (proc={proc_uid} creds={cred_uid})"
        );
        return Err(HelperError::Forbidden("caller uid mismatch".to_string()));
    }

    let exe = proc.exe().map_err(|e| {
        // Cross-UID /proc/<pid>/exe reads need CAP_SYS_PTRACE (see the old
        // systemd unit, deleted in Phase 4); if that capability grant is
        // ever lost, every caller silently gets rejected here. Log it -
        // this branch used to fail silently in the original Go
        // implementation, which is exactly what made the original "helper
        // not found" bug invisible in the journal.
        eprintln!("electric-eel: rejected call from pid {pid}: cannot resolve caller binary ({e})");
        HelperError::Forbidden("cannot resolve caller binary".to_string())
    })?;
    let exe = exe.to_string_lossy();

    if allowed_callers
        .iter()
        .any(|allowed| allowed == exe.as_ref())
    {
        eprintln!("electric-eel: authorized call from pid {pid} ({exe})");
        return Ok(());
    }
    eprintln!(
        "electric-eel: rejected call from pid {pid} ({exe}): not in ELECTRICEEL_ALLOWED_CALLERS"
    );
    Err(HelperError::Forbidden(format!(
        "caller not authorized: {exe}"
    )))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;

    #[test]
    fn test_split_and_trim() {
        let got = split_and_trim(" /usr/bin/harbour-electric-eel, /opt/foo ,, , /x ");
        assert_eq!(got, vec!["/usr/bin/harbour-electric-eel", "/opt/foo", "/x"]);
    }

    #[test]
    fn test_resolve_caller_paths() {
        let dir =
            std::env::temp_dir().join(format!("electric-eel-authtest-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let target = dir.join("target");
        let link = dir.join("link");
        std::fs::write(&target, b"x").unwrap();
        symlink(&target, &link).unwrap();

        let got = resolve_caller_paths(&[
            link.to_string_lossy().into_owned(),
            "/nonexistent/definitely/missing".to_string(),
        ]);
        assert_eq!(
            got,
            vec![
                target.to_string_lossy().into_owned(),
                "/nonexistent/definitely/missing".to_string(),
            ]
        );

        std::fs::remove_dir_all(&dir).ok();
    }
}
