/// Every subcommand tesla-control accepts (cmd/tesla-control/commands.go
/// upstream). `Run()` only ever execs one of these - never an arbitrary
/// string - so a compromised or buggy caller on the D-Bus system bus can't
/// smuggle in flags. A linear scan over ~50 short strings is simpler than a
/// HashSet/phf set here and the cost is irrelevant for a command that's
/// about to spawn a subprocess and wait seconds for it anyway.
pub(crate) const COMMAND_CATALOG: &[&str] = &[
    "valet-mode-on",
    "valet-mode-off",
    "unlock",
    "lock",
    "drive",
    "climate-on",
    "climate-off",
    "climate-set-temp",
    "add-key",
    "add-key-request",
    "remove-key",
    "rename-key",
    "list-keys",
    "honk",
    "ping",
    "flash-lights",
    "keep-accessory-power",
    "low-power-mode",
    "charging-set-limit",
    "charging-set-amps",
    "charging-start",
    "charging-stop",
    "charging-schedule",
    "charging-schedule-cancel",
    "charging-schedule-add",
    "charging-schedule-remove",
    "media-set-volume",
    "media-volume-up",
    "media-volume-down",
    "media-next-favorite",
    "media-next-track",
    "media-previous-track",
    "media-previous-favorite",
    "media-toggle-playback",
    "software-update-start",
    "software-update-cancel",
    "sentry-mode",
    "wake",
    "tonneau-open",
    "tonneau-close",
    "tonneau-stop",
    "trunk-open",
    "trunk-move",
    "trunk-close",
    "frunk-open",
    "charge-port-open",
    "charge-port-close",
    "autosecure-modelx",
    "session-info",
    "seat-heater",
    "steering-wheel-heater",
    "auto-seat-and-climate",
    "windows-vent",
    "windows-close",
    "body-controller-state",
    "guest-mode-on",
    "guest-mode-off",
    "erase-guest-data",
    "precondition-schedule-add",
    "precondition-schedule-remove",
    "product-info",
    "state",
];

/// Subcommands whose arguments carry a secret PIN. Their argument lists are
/// redacted from the audit log rather than written to the system journal in
/// cleartext. valet-mode-on is the only one in the pinned v0.4.1 binary -
/// verified by grepping `commands_vendor.go` for a "PIN" argument name; the
/// parental-controls-* entries once listed here were removed along with
/// their `COMMAND_CATALOG` entries (see below) since they don't exist
/// upstream at all.
pub(crate) const PIN_COMMANDS: &[&str] = &["valet-mode-on"];

pub(crate) fn is_known_command(cmd: &str) -> bool {
    COMMAND_CATALOG.contains(&cmd)
}

pub(crate) fn is_pin_command(cmd: &str) -> bool {
    PIN_COMMANDS.contains(&cmd)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;

    fn session_commands() -> HashSet<&'static str> {
        let source = include_str!("../session/commands_vendor.go");
        let source = source
            .split_once("var commands = map[string]*Command{")
            .unwrap()
            .1;
        let regex = regex::Regex::new(r#"(?m)^\t"([a-z0-9-]+)":\s*\{"#).unwrap();
        let commands: HashSet<_> = regex
            .captures_iter(source)
            .map(|capture| capture.get(1).unwrap().as_str())
            .filter(|command| !["get", "post"].contains(command))
            .collect();
        assert!(!commands.is_empty(), "session command extraction failed");
        commands
    }

    #[test]
    fn test_pin_commands_coverage() {
        let cmd = "valet-mode-on";
        assert!(
            is_pin_command(cmd),
            "pin-bearing command {cmd:?} not in PIN_COMMANDS"
        );
        assert!(
            is_known_command(cmd),
            "pin-bearing command {cmd:?} not in COMMAND_CATALOG"
        );
        for cmd in ["unlock", "honk", "trunk-open", "ping"] {
            assert!(
                !is_pin_command(cmd),
                "non-secret command {cmd:?} incorrectly marked as pin-bearing"
            );
        }
    }

    #[test]
    fn test_command_catalog_matches_upstream() {
        let known = session_commands();
        let unknown: Vec<&str> = COMMAND_CATALOG
            .iter()
            .copied()
            .filter(|c| !known.contains(c))
            .collect();
        assert!(
            unknown.is_empty(),
            "COMMAND_CATALOG lists commands that don't exist in the pinned \
             v0.4.1 tesla-control (would always fail with \"unrecognized \
             command\"): {unknown:?}"
        );
        let catalog: HashSet<_> = COMMAND_CATALOG.iter().copied().collect();
        assert_eq!(catalog, known, "Rust and session command catalogs differ");
    }

    // Same check, applied to the QML app's own command catalog - the two
    // are maintained by hand independently (see CommandCatalog.js's own
    // header comment on why they aren't generated from one source), so
    // they can drift from upstream, and from each other, in different
    // ways. This is what would have caught findings like
    // parental-controls-* before they shipped as always-erroring buttons.
    #[test]
    fn test_qml_catalog_matches_upstream() {
        let js = include_str!("../../app/qml/js/CommandCatalog.js");
        let re = regex::Regex::new(r#"cmd\("([a-z0-9-]+)""#).unwrap();
        let js_commands: HashSet<&str> = re
            .captures_iter(js)
            .map(|c| c.get(1).unwrap().as_str())
            .collect();
        assert!(
            !js_commands.is_empty(),
            "regex found zero cmd(...) calls in CommandCatalog.js - the \
             file moved, was renamed, or the extraction pattern above is \
             stale; fix the test before trusting a green run"
        );

        let known = session_commands();
        let mut unknown: Vec<&str> = js_commands
            .iter()
            .copied()
            .filter(|c| !known.contains(c))
            .collect();
        unknown.sort_unstable();
        assert!(
            unknown.is_empty(),
            "CommandCatalog.js lists commands that don't exist in the \
             pinned v0.4.1 tesla-control: {unknown:?}"
        );
    }
}
