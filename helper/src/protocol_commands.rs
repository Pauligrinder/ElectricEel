//! Parse application arguments once in Rust; Go receives only typed parameters.
use crate::tesla_ble::{Operation, Value};
use std::time::{SystemTime, UNIX_EPOCH};

fn integer(value: u64) -> Value {
    Value::Integer(value)
}
fn number(text: &str) -> Result<Value, String> {
    let value: f64 = text.trim().parse().map_err(|_| "expected a number")?;
    if !value.is_finite() {
        return Err("expected a finite number".into());
    }
    Ok(Value::Number(value))
}
fn unsigned(text: &str) -> Result<Value, String> {
    Ok(integer(
        text.trim()
            .parse()
            .map_err(|_| "expected a nonnegative integer")?,
    ))
}
fn on(text: &str) -> Result<Value, String> {
    match text.trim().to_ascii_lowercase().as_str() {
        "on" | "true" => Ok(integer(1)),
        "off" | "false" => Ok(integer(0)),
        _ => Err("expected on/off or true/false".into()),
    }
}
fn named(text: &str, names: &[(&str, u64)]) -> Result<Value, String> {
    names
        .iter()
        .find(|(name, _)| name.eq_ignore_ascii_case(text.trim()))
        .map(|(_, value)| integer(*value))
        .ok_or_else(|| "invalid enum value".into())
}
fn minutes(text: &str) -> Result<u64, String> {
    let (hour, minute) = text.split_once(':').ok_or("expected HH:MM")?;
    let hour: u64 = hour.parse().map_err(|_| "invalid hour")?;
    let minute: u64 = minute.parse().map_err(|_| "invalid minute")?;
    if hour > 23 || minute > 59 {
        return Err("invalid time".into());
    }
    Ok(hour * 60 + minute)
}
fn days(text: &str) -> Result<u64, String> {
    let mut mask = 0;
    for day in text.split(',') {
        mask |= match day.trim().to_ascii_lowercase().as_str() {
            "sun" | "sunday" => 1,
            "mon" | "monday" => 2,
            "tues" | "tuesday" => 4,
            "wed" | "wednesday" => 8,
            "thurs" | "thursday" => 16,
            "fri" | "friday" => 32,
            "sat" | "saturday" => 64,
            "all" => 127,
            "weekdays" => 62,
            _ => return Err("invalid schedule day".into()),
        };
    }
    Ok(mask)
}
fn arity(args: &[String], min: usize, max: usize) -> Result<(), String> {
    if (min..=max).contains(&args.len()) {
        Ok(())
    } else {
        Err("invalid argument count".into())
    }
}

#[allow(clippy::too_many_lines)] // Exhaustive typed mapping of the app command catalogue.
pub(crate) fn parse(cmd: &str, args: &[String]) -> Result<Operation, String> {
    if cmd == "navigate" {
        return match args.first().map(String::as_str) {
            Some("gps") => {
                arity(args, 3, 3)?;
                let latitude = args[1].parse().map_err(|_| "invalid latitude")?;
                let longitude = args[2].parse().map_err(|_| "invalid longitude")?;
                if !crate::share::valid_latlon(latitude, longitude) {
                    return Err("invalid navigation coordinates".into());
                }
                Ok(Operation::NavigateGps {
                    latitude,
                    longitude,
                })
            }
            Some("address") => {
                arity(args, 2, 2)?;
                Ok(Operation::NavigateAddress(args[1].clone()))
            }
            _ => Err("invalid navigation destination".into()),
        };
    }
    let index = crate::commands::COMMAND_CATALOG
        .iter()
        .position(|name| *name == cmd)
        .ok_or("unknown command")?;
    let values = match cmd {
        "rename-key" | "product-info" => {
            return Err("operation requires Fleet API and is unavailable over BLE".into())
        }
        "valet-mode-on" => {
            arity(args, 1, 1)?;
            vec![Value::Bytes(args[0].as_bytes().to_vec())]
        }
        "climate-set-temp" => {
            arity(args, 1, 1)?;
            let text = args[0].trim();
            let fahrenheit = text.ends_with(['f', 'F']);
            let number_text = text.trim_end_matches(['c', 'C', 'f', 'F']);
            let value: f64 = number_text.parse().map_err(|_| "invalid temperature")?;
            vec![Value::Number(if fahrenheit {
                (value - 32.0) * 5.0 / 9.0
            } else {
                value
            })]
        }
        "keep-accessory-power" | "low-power-mode" | "sentry-mode" | "steering-wheel-heater" => {
            arity(args, 1, 1)?;
            vec![on(&args[0])?]
        }
        "charging-set-limit" | "charging-set-amps" | "charging-schedule" => {
            arity(args, 1, 1)?;
            vec![unsigned(&args[0])?]
        }
        "media-set-volume" => {
            arity(args, 1, 1)?;
            vec![number(&args[0])?]
        }
        "software-update-start" => {
            arity(args, 1, 1)?;
            let text = args[0].trim();
            let (text, multiplier) = if let Some(t) = text.strip_suffix('h') {
                (t, 3600.0)
            } else if let Some(t) = text.strip_suffix('m') {
                (t, 60.0)
            } else {
                (text.strip_suffix('s').unwrap_or(text), 1.0)
            };
            let seconds: f64 = text.parse().map_err(|_| "invalid delay")?;
            let seconds = seconds * multiplier;
            if !(0.0..=3600.0).contains(&seconds) {
                return Err("invalid delay".into());
            }
            vec![integer(
                std::time::Duration::from_secs_f64(seconds)
                    .as_nanos()
                    .try_into()
                    .map_err(|_| "delay overflow")?,
            )]
        }
        "add-key" | "add-key-request" => {
            arity(args, 3, 3)?;
            vec![
                Value::Bytes(crate::keys::public_bytes(std::path::Path::new(&args[0]))?),
                named(
                    &args[1],
                    &[
                        ("owner", 2),
                        ("driver", 3),
                        ("fm", 4),
                        ("vehicle_monitor", 5),
                        ("charging_manager", 6),
                    ],
                )?,
                named(
                    &args[2],
                    &[
                        ("nfc_card", 1),
                        ("ios_device", 6),
                        ("android_device", 7),
                        ("cloud_key", 9),
                    ],
                )?,
            ]
        }
        "remove-key" => {
            arity(args, 1, 1)?;
            vec![Value::Bytes(crate::keys::public_bytes(
                std::path::Path::new(&args[0]),
            )?)]
        }
        "session-info" => {
            arity(args, 2, 2)?;
            vec![
                Value::Bytes(crate::keys::public_bytes(std::path::Path::new(&args[0]))?),
                named(&args[1], &[("vcsec", 1), ("infotainment", 2)])?,
            ]
        }
        "seat-heater" => {
            arity(args, 2, 2)?;
            vec![
                named(
                    &args[0],
                    &[
                        ("front-left", 1),
                        ("front-right", 2),
                        ("2nd-row-left", 3),
                        ("2nd-row-center", 5),
                        ("2nd-row-right", 6),
                        ("3rd-row-left", 8),
                        ("3rd-row-right", 9),
                    ],
                )?,
                named(
                    &args[1],
                    &[("off", 0), ("low", 1), ("medium", 2), ("high", 3)],
                )?,
            ]
        }
        "auto-seat-and-climate" => {
            arity(args, 1, 2)?;
            let mask = match args[0].trim().to_ascii_uppercase().as_str() {
                "L" => 1,
                "R" => 2,
                "LR" | "RL" => 3,
                _ => return Err("invalid seat selection".into()),
            };
            vec![
                integer(mask),
                on(args
                    .get(1)
                    .filter(|value| !value.is_empty())
                    .map_or("on", String::as_str))?,
            ]
        }
        "state" => {
            arity(args, 1, 1)?;
            vec![named(
                &args[0],
                &[
                    ("charge", 0),
                    ("climate", 1),
                    ("drive", 2),
                    ("location", 3),
                    ("closures", 4),
                    ("charge-schedule", 5),
                    ("precondition-schedule", 6),
                    ("tire-pressure", 7),
                    ("media", 8),
                    ("media-detail", 9),
                    ("software-update", 10),
                    ("parental-controls", 11),
                ],
            )?]
        }
        "charging-schedule-remove" | "precondition-schedule-remove" => {
            arity(args, 1, 2)?;
            let kind = match args[0].trim().to_ascii_lowercase().as_str() {
                "home" => 0,
                "work" => 1,
                "other" => 2,
                "id" => 3,
                _ => return Err("invalid schedule selection".into()),
            };
            let id = if kind == 3 {
                unsigned(args.get(1).ok_or("missing schedule ID")?)?
            } else {
                integer(0)
            };
            vec![integer(kind), id]
        }
        "charging-schedule-add" | "precondition-schedule-add" => {
            arity(args, 4, 7)?;
            let repeat = args
                .get(4)
                .filter(|arg| !arg.is_empty())
                .map_or("weekly", String::as_str);
            let once = match repeat.to_ascii_lowercase().as_str() {
                "once" => 1,
                "weekly" => 0,
                _ => return Err("invalid repeat policy".into()),
            };
            let id = if let Some(id) = args.get(5).filter(|id| !id.is_empty()) {
                unsigned(id)?
            } else {
                integer(
                    SystemTime::now()
                        .duration_since(UNIX_EPOCH)
                        .map_err(|_| "invalid system clock")?
                        .as_nanos()
                        .try_into()
                        .map_err(|_| "schedule ID overflow")?,
                )
            };
            let enabled = on(args
                .get(6)
                .filter(|arg| !arg.is_empty())
                .map_or("true", String::as_str))?;
            let mut values = vec![id, integer(days(&args[0])?)];
            if cmd == "charging-schedule-add" {
                let (start, end) = args[1].split_once('-').ok_or("invalid time interval")?;
                if start.is_empty() && end.is_empty() {
                    return Err("empty time interval".into());
                }
                values.extend([
                    integer(if start.is_empty() { 0 } else { minutes(start)? }),
                    integer(if end.is_empty() { 0 } else { minutes(end)? }),
                    integer(u64::from(!start.is_empty())),
                    integer(u64::from(!end.is_empty())),
                ]);
            } else {
                values.extend([integer(minutes(&args[1])?), integer(0)]);
            }
            values.extend([number(&args[2])?, number(&args[3])?, integer(once), enabled]);
            values
        }
        _ => {
            arity(args, 0, 0)?;
            Vec::new()
        }
    };
    Ok(Operation::Command {
        command: u32::try_from(index + 1).map_err(|_| "command ID overflow")?,
        values,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn optional_schedule_slots_preserve_end_only_time_and_existing_id() {
        let args = ["weekdays", "-06:00", "-33.4", "151.2", "", "123", "false"].map(str::to_string);
        let Operation::Command { values, .. } = parse("charging-schedule-add", &args).unwrap()
        else {
            panic!("command")
        };
        assert!(matches!(values[0], Value::Integer(123)));
        assert!(matches!(values[3], Value::Integer(360)));
        assert!(matches!(values[4], Value::Integer(0)));
        assert!(matches!(values[5], Value::Integer(1)));
        assert!(matches!(values[9], Value::Integer(0)));
    }
    #[test]
    fn every_zero_argument_ble_command_has_a_typed_mapping() {
        for command in [
            "lock",
            "unlock",
            "drive",
            "honk",
            "body-controller-state",
            "windows-close",
            "guest-mode-on",
            "list-keys",
        ] {
            assert!(parse(command, &[]).is_ok(), "{command}");
        }
        assert!(parse("lock", &["extra".into()]).is_err());
        assert!(parse("product-info", &[]).is_err());
    }
    #[test]
    fn production_findings_navigate_gps_rejects_nonfinite_and_out_of_range() {
        // share.rs valid_latlon requires finite lat in [-90,90] and lon in
        // [-180,180]. parse("navigate",["gps",..]) currently accepts
        // inf/NaN/out-of-range via a bare f64 parse, letting an invalid
        // action reach BLE. Must fail fast like share_destination does.
        for args in [
            ["gps", "inf", "0"],
            ["gps", "-inf", "0"],
            ["gps", "NaN", "0"],
            ["gps", "91", "0"],
            ["gps", "48.85", "181"],
        ] {
            let owned = args.map(str::to_string);
            assert!(
                parse("navigate", &owned).is_err(),
                "navigate {args:?} must be rejected before BLE"
            );
        }
    }
}
