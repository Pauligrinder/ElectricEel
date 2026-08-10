/// Every subcommand tesla-control accepts (cmd/tesla-control/commands.go
/// upstream). Run() only ever execs one of these - never an arbitrary
/// string - so a compromised or buggy caller on the D-Bus system bus can't
/// smuggle in flags. A linear scan over ~50 short strings is simpler than a
/// HashSet/phf set here and the cost is irrelevant for a command that's
/// about to spawn a subprocess and wait seconds for it anyway.
pub const COMMAND_CATALOG: &[&str] = &[
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
    "parental-controls-on",
    "parental-controls-off",
    "parental-controls-set-speed-limit",
    "parental-controls-enable-setting",
    "parental-controls-clear-pin-admin",
    "precondition-schedule-add",
    "precondition-schedule-remove",
    "product-info",
    "state",
];

/// Subcommands whose arguments carry a secret PIN. Their argument lists are
/// redacted from the audit log rather than written to the system journal in
/// cleartext.
pub const PIN_COMMANDS: &[&str] = &[
    "valet-mode-on",
    "parental-controls-on",
    "parental-controls-off",
    "parental-controls-clear-pin-admin",
];

pub fn is_known_command(cmd: &str) -> bool {
    COMMAND_CATALOG.contains(&cmd)
}

pub fn is_pin_command(cmd: &str) -> bool {
    PIN_COMMANDS.contains(&cmd)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_pin_commands_coverage() {
        for cmd in [
            "valet-mode-on",
            "parental-controls-on",
            "parental-controls-off",
            "parental-controls-clear-pin-admin",
        ] {
            assert!(
                is_pin_command(cmd),
                "pin-bearing command {cmd:?} not in PIN_COMMANDS"
            );
            assert!(
                is_known_command(cmd),
                "pin-bearing command {cmd:?} not in COMMAND_CATALOG"
            );
        }
        for cmd in ["unlock", "honk", "trunk-open", "ping"] {
            assert!(
                !is_pin_command(cmd),
                "non-secret command {cmd:?} incorrectly marked as pin-bearing"
            );
        }
    }
}
