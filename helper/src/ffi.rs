//! C ABI used by the Qt UI (`app/src/teslaclient.cpp`). The header
//! `electriceelcore.h` is generated from this module by `build.rs` (cbindgen).
//!
//! The Rust application entrypoint (`app.rs`) owns the runtime; Qt only queues
//! JSON actions with `runtime_submit`, receives pushed notifications through
//! `runtime_observe`, and reads the build version with `core_version`.

use std::ffi::CStr;
use std::os::raw::c_char;

/// Queue one typed JSON UI action without blocking on vehicle work.
/// # Safety
/// `runtime` must be live; `request` must be a NUL-terminated UTF-8 string.
#[cfg(feature = "runtime")]
#[no_mangle]
pub unsafe extern "C" fn runtime_submit(
    runtime: *mut crate::runtime::Runtime,
    request: *const c_char,
) -> bool {
    let Some(runtime) = (unsafe { runtime.as_ref() }) else {
        return false;
    };
    if request.is_null() {
        return false;
    }
    // SAFETY: caller passes a NUL-terminated string.
    unsafe { CStr::from_ptr(request) }
        .to_str()
        .is_ok_and(|request| runtime.submit(request))
}

/// Attach a UI sink, or detach with NULL. Detachment waits for in-flight
/// callbacks, so the UI can safely destroy its context after this returns.
/// # Safety
/// `runtime` must be live. The callback must copy its borrowed JSON and return
/// immediately, without re-entering this ABI. `context` stays live until detach.
#[cfg(feature = "runtime")]
#[no_mangle]
pub unsafe extern "C" fn runtime_observe(
    runtime: *mut crate::runtime::Runtime,
    callback: Option<unsafe extern "C" fn(*mut std::ffi::c_void, *const c_char)>,
    context: *mut std::ffi::c_void,
) {
    if let Some(runtime) = unsafe { runtime.as_ref() } {
        unsafe { runtime.observe(callback, context) };
    }
}

/// The build version of the core, stamped from `CARGO_PKG_VERSION` (same git
/// tag as `APP_VERSION` in harbour-electric-eel.pro). Static storage - do not
/// free.
#[no_mangle]
pub extern "C" fn core_version() -> *const c_char {
    const VERSION: &CStr =
        match CStr::from_bytes_with_nul(concat!(env!("CARGO_PKG_VERSION"), "\0").as_bytes()) {
            Ok(version) => version,
            Err(_) => panic!("CARGO_PKG_VERSION contains a NUL byte"),
        };
    VERSION.as_ptr()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_version_is_c_string() {
        // SAFETY: static, never freed.
        let v = unsafe { CStr::from_ptr(core_version()) }.to_str().unwrap();
        assert_eq!(v, env!("CARGO_PKG_VERSION"));
    }

    #[cfg(feature = "runtime")]
    #[test]
    fn null_runtime_and_request_are_rejected() {
        // SAFETY: NULL handles are explicitly tolerated.
        assert!(!unsafe { runtime_submit(std::ptr::null_mut(), c"{}".as_ptr()) });
        unsafe { runtime_observe(std::ptr::null_mut(), None, std::ptr::null_mut()) };
    }
}
