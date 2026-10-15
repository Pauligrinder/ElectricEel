//! In-process control core for harbour-electric-eel (see `docs/architecture.md`).
//!
//! Built as a staticlib linked into the app. With `app-entry` it provides the
//! Rust application entrypoint: Rust owns the runtime and its threads, while
//! Qt only submits actions and renders notifications through the small C ABI
//! in `ffi.rs`. The rlib form exists for the crate's own tests.

// Without `app-entry` nothing in the crate constructs the core (only tests
// and the C ABI exist), so most items have no non-test caller there.
#![cfg_attr(not(feature = "app-entry"), allow(dead_code))]

pub mod commands;
pub mod config;
pub mod core;
mod cover_action;
pub mod error;
pub mod ffi;
pub mod keylog;
mod keys;
mod phone_key;
mod protocol_client;
mod protocol_commands;
pub mod share;
pub mod tesla_ble;

#[cfg(feature = "runtime")]
mod automagic;
#[cfg(feature = "runtime")]
mod cpukeepalive;
#[cfg(feature = "runtime")]
mod notification;
#[cfg(feature = "runtime")]
pub mod runtime;

#[cfg(all(feature = "app-entry", not(test)))]
mod app;

#[cfg(test)]
mod review_round3_tests;
