use std::fmt;
use std::fs;
use std::io::{self, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::sync::LazyLock;

use regex::Regex;
use serde::{Deserialize, Serialize};
use tempfile::NamedTempFile;

pub const MAX_TIMEOUT_SEC: i32 = 300;

static VIN_RE: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^[A-HJ-NPR-Z0-9]{17}$").unwrap());

/// Validation failure for a SetConfig payload.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConfigError {
    PositiveTimeout,
    MaxTimeout,
    InvalidVin,
}

impl fmt::Display for ConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            ConfigError::PositiveTimeout => write!(f, "timeouts must be positive"),
            ConfigError::MaxTimeout => write!(f, "timeouts must be <= {MAX_TIMEOUT_SEC}"),
            ConfigError::InvalidVin => {
                write!(f, "invalid VIN format (17 alphanumeric chars, no I/O/Q)")
            }
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Config {
    pub vin: String,
    pub key_name: String,
    pub connect_timeout_sec: i32,
    pub command_timeout_sec: i32,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            vin: String::new(),
            key_name: "harbour-teslacontrol".to_string(),
            connect_timeout_sec: 20,
            command_timeout_sec: 5,
        }
    }
}

impl Config {
    /// Reads the config, returning an I/O error if the file exists but can't
    /// be read. A missing file is the caller's signal to fall back to
    /// [`Config::default`]; an unparseable file is logged and defaults too.
    pub fn load(path: &Path) -> io::Result<Config> {
        let data = match fs::read(path) {
            Ok(d) => d,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(Config::default()),
            Err(e) => return Err(e),
        };
        match serde_json::from_slice(&data) {
            Ok(cfg) => Ok(cfg),
            Err(e) => {
                eprintln!(
                    "teslacontrold: ignoring unparseable config {}: {}",
                    path.display(),
                    e
                );
                Ok(Config::default())
            }
        }
    }

    /// Writes to a `.tmp` sibling and renames into place, so a crash mid-write
    /// can't truncate/zero the file and silently reset the VIN/key/timeouts.
    /// Both the temp file's contents (fsync before rename) and the parent
    /// directory entry (fsync after rename) are flushed to disk, so a power
    /// loss right after the rename can't lose the new config either.
    pub fn save(&self, path: &Path) -> io::Result<()> {
        let data = serde_json::to_vec_pretty(self)?;
        let dir = path.parent().unwrap_or_else(|| Path::new("."));
        let mut tmp = NamedTempFile::new_in(dir)?;
        tmp.as_file()
            .set_permissions(fs::Permissions::from_mode(0o600))?;
        tmp.write_all(&data)?;
        tmp.as_file().sync_all()?;
        tmp.persist(path)?;
        fs::File::open(dir)?.sync_all()
    }
}

/// Returns `Ok(())` if the inputs are acceptable, else a human-readable error.
/// Shared by SetConfig and unit tests so the bounds can be verified without a
/// live D-Bus connection.
pub fn validate_config(
    vin: &str,
    connect_timeout: i32,
    command_timeout: i32,
) -> Result<(), ConfigError> {
    if connect_timeout <= 0 || command_timeout <= 0 {
        return Err(ConfigError::PositiveTimeout);
    }
    if connect_timeout > MAX_TIMEOUT_SEC || command_timeout > MAX_TIMEOUT_SEC {
        return Err(ConfigError::MaxTimeout);
    }
    let vin = vin.trim();
    if !vin.is_empty() && !VIN_RE.is_match(vin) {
        return Err(ConfigError::InvalidVin);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_validate_config() {
        let valid_vin = "5YJ3E1EA0PF000000";
        let cases: &[(&str, &str, i32, i32, Option<ConfigError>)] = &[
            ("all valid", valid_vin, 20, 5, None),
            (
                "zero connect timeout",
                valid_vin,
                0,
                5,
                Some(ConfigError::PositiveTimeout),
            ),
            (
                "negative command timeout",
                valid_vin,
                20,
                -1,
                Some(ConfigError::PositiveTimeout),
            ),
            (
                "connect timeout too large",
                valid_vin,
                MAX_TIMEOUT_SEC + 1,
                5,
                Some(ConfigError::MaxTimeout),
            ),
            (
                "command timeout too large",
                valid_vin,
                20,
                MAX_TIMEOUT_SEC + 1,
                Some(ConfigError::MaxTimeout),
            ),
            (
                "exactly at max allowed",
                valid_vin,
                MAX_TIMEOUT_SEC,
                MAX_TIMEOUT_SEC,
                None,
            ),
            ("empty VIN clears config", "", 20, 5, None),
            (" 5YJ3E1EA0PF000000 ", " 5YJ3E1EA0PF000000 ", 20, 5, None),
            (
                "VIN too short",
                "5YJ3E1EA0PF00000",
                20,
                5,
                Some(ConfigError::InvalidVin),
            ),
            (
                "VIN with letter I",
                "5YJ3E1EA0PI000000",
                20,
                5,
                Some(ConfigError::InvalidVin),
            ),
            (
                "VIN with letter O",
                "5YJ3E1EA0PO000000",
                20,
                5,
                Some(ConfigError::InvalidVin),
            ),
            (
                "VIN with lowercase",
                "5yj3e1ea0pf000000",
                20,
                5,
                Some(ConfigError::InvalidVin),
            ),
        ];
        for (name, vin, connect_timeout, command_timeout, want) in cases {
            let got = validate_config(vin, *connect_timeout, *command_timeout).err();
            assert_eq!(got, *want, "{name}: validate_config({vin:?})");
        }
    }

    #[test]
    fn test_save_config_atomic() {
        let dir = std::env::temp_dir().join(format!("teslacontrold-test-{}", std::process::id()));
        fs::create_dir_all(&dir).unwrap();
        let path = dir.join("config.json");

        let cfg = Config {
            vin: "5YJ3E1EA0PF000000".to_string(),
            key_name: "harbour-teslacontrol".to_string(),
            connect_timeout_sec: 20,
            command_timeout_sec: 5,
        };
        cfg.save(&path).expect("save");

        let data = fs::read_to_string(&path).expect("config file not written");
        assert!(!data.is_empty(), "config file is empty");

        let leftover = fs::read_dir(&dir)
            .unwrap()
            .filter_map(Result::ok)
            .filter(|e| e.file_name() != "config.json")
            .count();
        assert_eq!(leftover, 0, "temporary file left behind after rename");

        let perm = fs::metadata(&path).unwrap().permissions().mode() & 0o777;
        assert_eq!(perm, 0o600, "config file permissions");

        let reloaded = Config::load(&path).expect("reload");
        assert_eq!(reloaded.vin, cfg.vin);
        assert_eq!(reloaded.connect_timeout_sec, cfg.connect_timeout_sec);

        fs::remove_dir_all(&dir).ok();
    }
}
