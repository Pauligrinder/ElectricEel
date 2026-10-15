//! Review round 3 regressions. Every test here fails on the reviewed code and
//! documents the desired behavior; see the comment above each test.
//!
//! Tests that need the production protocol path use `tesla_ble::fake`, an
//! in-process stand-in for the Go ABI, instead of the legacy subprocess
//! transport that the Harbour application never compiles.

use std::path::Path;
use std::time::Duration;

use crate::config::{Config, VinState};
use crate::core::Core;
use crate::cover_action::CoverAction;
use crate::error::HelperError;
use crate::protocol_client::ProtocolClient;
use crate::share::{parse_shared_text, Destination};
use crate::tesla_ble::fake;

/// A core backed by the production `ProtocolClient` over the fake library.
fn fake_core(dir: &Path, vin: &str, state: VinState) -> Core {
    let cfg = Config {
        vin: vin.into(),
        vin_state: state,
        ..Config::default()
    };
    cfg.save(&dir.join("config.json")).unwrap();
    crate::keys::generate(
        &dir.join("private_key.pem"),
        &dir.join("public_key.pem"),
        false,
    )
    .unwrap();
    Core::new(dir, Some(ProtocolClient::with_library(fake::library()))).unwrap()
}

// BUG (core.rs `set_config_with_cover_actions`, ~line 1009): every Settings
// save stops phone-key presence, invalidates the protocol session (tearing
// down the authenticated BLE link and the Go session) and restarts it, even
// when nothing that the session depends on changed. Saving a cosmetic
// preference (cover buttons, model picture) while sitting in the car drops
// the phone-key link and flaps the MCE CPU lease. The "argv baked into the
// child" rationale in the comment only applied to the removed subprocess.
// Desired: only VIN or timeout changes retire the session; cosmetic or
// identical saves keep the running presence session (same generation).
#[test]
fn cosmetic_settings_save_keeps_the_live_phone_key_session() {
    let dir = tempfile::tempdir().unwrap();
    let vin = "5YJ3E1EA0PF300001";
    let core = fake_core(dir.path(), vin, VinState::Paired);
    core.start_phone_key().unwrap();
    assert!(core.phone_key_enabled());
    let generation = core.session_generation();

    core.set_config_with_cover_actions(
        vin,
        "model3",
        "harbour-electric-eel",
        20,
        5,
        Some((Some(CoverAction::Lock), Some(CoverAction::Unlock))),
    )
    .unwrap();

    assert_eq!(
        core.session_generation(),
        generation,
        "a cover/model-only save must not tear down the phone-key session"
    );
    core.stop_phone_key();
}

// BUG (protocol_client.rs `controller`/`maintenance`, tesla_ble.rs callback
// kind 4): raw observations are drained only by the presence controller.
// With phone-key mode off (never paired, or unpaired) every unexpected drop
// of a foreground command link (lock + walk away) still queues an
// EE_DISCONNECTED observation that nobody consumes. The 65th one trips the
// 64-entry overflow guard and faults the session ("protocol observation
// queue overflow"): any in-flight operation completes as InternalBug and the
// Go session has to be torn down and rebuilt. Stale observations are also
// replayed to the controller when presence later starts on the same session.
// Desired: observations are drained/discarded while presence is off, so an
// idle session stays healthy no matter how many link drops it has seen.
#[test]
fn idle_session_survives_link_drops_while_phone_key_is_off() {
    let dir = tempfile::tempdir().unwrap();
    let vin = "5YJ3E1EA0PF300002";
    let private = dir.path().join("private_key.pem");
    crate::keys::generate(&private, &dir.path().join("public_key.pem"), false).unwrap();
    let client = ProtocolClient::with_library(fake::library());
    let key = private.to_string_lossy().into_owned();
    let honk = crate::protocol_commands::parse(crate::commands::Command::Honk, &[]).unwrap();
    let outcome = client
        .run(crate::commands::Command::Honk, honk, vin, &key, 20, 5)
        .unwrap();
    assert!(outcome.ok, "fake library command must succeed");
    assert!(client.is_alive());

    // Two bursts below the queue bound, separated by ample time for any
    // background drain, model link drops spread over days of normal use.
    for _ in 0..2 {
        for _ in 0..63 {
            fake::observe(
                vin,
                fake::ObservationKind::Disconnected,
                fake::NOT_CONNECTED,
            );
        }
        std::thread::sleep(Duration::from_millis(500));
    }

    assert!(
        client.is_alive(),
        "unconsumed link-drop observations faulted an idle protocol session"
    );
}

// BUG (protocol_client.rs `run`, protocol_commands.rs `parse`): argument
// errors from the typed Rust parser are reported as
// `HelperError::SessionUnavailable("bluez persistent session failed for
// ...")`, i.e. as a transport failure, although nothing touched the session.
// The UI tells the user the BLE session is broken for what is a bad argument.
// Desired: a parse failure surfaces as `HelperError::InvalidArgument`, like
// every other argument validation in `Core::run`.
#[test]
fn typed_argument_errors_are_reported_as_invalid_arguments() {
    let dir = tempfile::tempdir().unwrap();
    let core = fake_core(dir.path(), "5YJ3E1EA0PF300003", VinState::Unpaired);
    let result = core.run("seat-heater", &["bogus-seat".into(), "high".into()]);
    assert!(
        matches!(result, Err(HelperError::InvalidArgument(_))),
        "bad enum argument misreported as {result:?}"
    );
}

// BUG (core.rs `validate_arg_ranges` vs protocol_commands.rs `parse`): the two
// independent argument parsers disagree. The validator trims the number
// before a unit suffix and accepts "21 C", but the typed parser does
// `trim_end_matches(['c','C','f','F'])` and then parses "21 " without
// trimming, so a value the gatekeeper approved fails afterwards (and, per the
// previous finding, as "session unavailable").
// Desired: anything `Core::run` validates is executed; "21 C" means 21 °C.
#[test]
fn temperature_accepted_by_validator_is_accepted_by_typed_parser() {
    let dir = tempfile::tempdir().unwrap();
    let core = fake_core(dir.path(), "5YJ3E1EA0PF300004", VinState::Unpaired);
    let result = core.run("climate-set-temp", &["21 C".into()]);
    assert!(
        result.as_ref().is_ok_and(|outcome| outcome.ok),
        "validated temperature was rejected later: {result:?}"
    );
}

// BUG (share.rs `parse_geo_uri`): RFC 5870 allows an optional third
// coordinate (altitude): `geo:48.2010,16.3695,183`. The strict pair parser
// sees three numbers, returns None, and the caller reports
// `ShareParseError::Empty` ("nothing to share: the text is empty").
// Desired: altitude is ignored and the point is used.
#[test]
fn geo_uri_with_altitude_is_a_point() {
    assert_eq!(
        parse_shared_text("geo:48.2010,16.3695,183"),
        Ok(Destination::LatLon {
            lat: 48.201,
            lon: 16.3695
        })
    );
}

// BUG (share.rs `numeric_pair`/`first_url_token`): the standard Android map
// intent form `geo:0,0?q=lat,lng(label)` and the equivalent Google
// `?q=lat,lng(label)` are not recognized as coordinates. The geo form is sent
// to the car geocoder as the literal "37.423,-122.084(Google)"; for the URL,
// `first_url_token` also strips the closing parenthesis, producing the
// mangled address "45.1,9.2(Home".
// Desired: a trailing `(label)` after a coordinate pair is ignored.
#[test]
fn coordinate_query_with_label_is_a_point() {
    assert_eq!(
        parse_shared_text("geo:0,0?q=37.423,-122.084(Google)"),
        Ok(Destination::LatLon {
            lat: 37.423,
            lon: -122.084
        })
    );
    assert_eq!(
        parse_shared_text("https://maps.google.com/?q=45.1,9.2(Home)"),
        Ok(Destination::LatLon {
            lat: 45.1,
            lon: 9.2
        })
    );
}

// BUG (share.rs `slash_run_coords`): the host-agnostic "exactly two
// bare-numeric path segments" rule fires on ordinary, non-map URLs.
// A forum/blog link `/forum/12/34/thread` silently becomes the point
// (12, 34) in Chad and is sent to the car; a dated article
// `/news/2024/05/...` fails with "coordinates out of range". This
// contradicts `parse_map_url`'s contract ("Never errors on opaque URLs").
// (The run only matches when another segment or the appended '#' follows,
// so `/12/34` alone stays opaque, which shows the rule is accidental.)
// Desired: such URLs stay opaque (`Address(url)`); slash runs should only be
// trusted in a recognized map fragment such as OSM `#map=z/lat/lon`.
#[test]
fn numeric_path_segments_of_non_map_urls_stay_opaque() {
    for url in [
        "https://example.com/forum/12/34/thread",
        "https://example.com/news/2024/05/new-supercharger",
    ] {
        assert_eq!(
            parse_shared_text(url),
            Ok(Destination::Address(url.into())),
            "{url}"
        );
    }
}

// BUG (share.rs `parse_shared_text`/`parse_map_url`/`parse_geo_uri`): address
// text can carry control characters (a TAB inside the shared line, or a
// percent-decoded %0A/%09 inside `q=`). Preview reports success, but Go's
// NavigateAddress rejects any `unicode.IsControl` rune with
// "invalid destination text" - after the car has already been woken and an
// authenticated BLE session established.
// Desired: the single authoritative parser never yields an address the
// protocol will reject (normalize control characters to spaces, or reject).
#[test]
fn address_destinations_never_contain_control_characters() {
    for text in [
        "Via Roma 1\tMilano",
        "https://maps.google.com/?q=Via+Roma%0AMilano",
        "geo:0,0?q=Via%09Roma",
    ] {
        let destination = parse_shared_text(text);
        if let Ok(Destination::Address(address)) = &destination {
            assert!(
                !address.chars().any(char::is_control),
                "{text:?} produced {destination:?}, which the protocol rejects"
            );
        }
    }
}

// BUG (protocol_client.rs `ensure`/`invalidate`/`stop_presence`): the old
// session was stopped, joined and dropped - and `Session::drop` blocks in
// Go's destroy until backend teardown completes - while the `inner.session`
// mutex was held. `cancel()`, which the Qt GUI thread reaches through
// `runtime_submit("shutdown")`, needs that same mutex, so the UI froze for
// the whole teardown (forever if Go's teardown is stuck).
// Desired: teardown happens after the session leaves its slot; `cancel()`
// returns promptly.
#[test]
fn cancel_does_not_wait_for_a_session_teardown() {
    use std::time::Instant;
    let dir = tempfile::tempdir().unwrap();
    let vin = "5YJ3E1EA0PF300006";
    fake::script(
        vin,
        fake::Script {
            destroy_delay: Duration::from_secs(2),
            ..fake::Script::default()
        },
    );
    let private = dir.path().join("private_key.pem");
    crate::keys::generate(&private, &dir.path().join("public_key.pem"), false).unwrap();
    let client = std::sync::Arc::new(ProtocolClient::with_library(fake::library()));
    let key = private.to_string_lossy().into_owned();
    let honk = crate::protocol_commands::parse(crate::commands::Command::Honk, &[]).unwrap();
    assert!(
        client
            .run(crate::commands::Command::Honk, honk, vin, &key, 20, 5)
            .unwrap()
            .ok
    );
    let teardown = std::thread::spawn({
        let client = std::sync::Arc::clone(&client);
        move || client.invalidate()
    });
    std::thread::sleep(Duration::from_millis(300));
    let start = Instant::now();
    client.cancel();
    let blocked = start.elapsed();
    teardown.join().unwrap();
    assert!(
        blocked < Duration::from_millis(500),
        "cancel blocked {blocked:?} behind a session teardown"
    );
}

// BUG (config.rs `write_atomic`): for a bare file name `Path::parent()` is
// `Some("")`, so after the rename succeeded the directory fsync opened ""
// and failed: a durable write was reported as an error.
// Desired: a bare file name means the current directory.
#[test]
fn atomic_write_to_a_bare_file_name_succeeds() {
    let name = format!("review-round3-atomic-{}.tmp", std::process::id());
    let path = Path::new(&name);
    let result = crate::config::write_atomic(path, b"data");
    let written = std::fs::read(path);
    let _ = std::fs::remove_file(path);
    assert!(result.is_ok(), "{result:?}");
    assert_eq!(written.unwrap(), b"data");
}
