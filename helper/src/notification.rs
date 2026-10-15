//! Typed runtime messages. JSON is produced only at the Qt C-ABI boundary.
use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum PhoneKeyLink {
    Unpaired,
    BluetoothOff,
    Scanning,
    Connected,
    Authorized,
    Stopped,
    Error,
}

#[derive(Clone, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub(crate) enum Notification {
    Initialized {
        ok: bool,
    },
    PhoneKeyState {
        active: bool,
        link: PhoneKeyLink,
        status: String,
        generation: u64,
    },
    PhoneKeyEvent {
        kind: String,
        vin: String,
        time: String,
        error: String,
        // Session identity is internal routing metadata, not a public D-Bus arg.
        #[serde(skip)]
        generation: u64,
    },
    StatusRefreshRequested,
    AutomagicExportFinished {
        ok: bool,
        path: String,
        error: String,
    },
    CommandFinished {
        request_id: String,
        ok: bool,
        stdout: String,
        stderr: String,
        exit_code: i32,
    },
    CommandError {
        request_id: String,
        error: String,
    },
    KeyGenerated {
        ok: bool,
        pem: String,
        error: String,
    },
    Paired {
        ok: bool,
        output: String,
        error: String,
    },
    ConfigSaved {
        ok: bool,
        error: String,
    },
    ConfigLoaded {
        vin: String,
        model: String,
        key_name: String,
        connect_timeout_sec: i32,
        command_timeout_sec: i32,
        has_key: bool,
        pem: String,
        cover_left_action: crate::cover_action::CoverAction,
        cover_right_action: crate::cover_action::CoverAction,
    },
    DestinationPreviewed {
        request_id: String,
        ok: bool,
        kind: String,
        value1: String,
        value2: String,
        error: String,
    },
    ShareFinished {
        request_id: String,
        ok: bool,
        output: String,
        error: String,
    },
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn wire_tags_fields_and_private_metadata_match_qt_contract() {
        let event = Notification::PhoneKeyEvent {
            kind: "presence_away_confirmed".into(),
            vin: "VIN".into(),
            time: "TIME".into(),
            error: String::new(),
            generation: 42,
        };
        assert_eq!(
            serde_json::to_value(event).unwrap(),
            json!({
                "type":"phone_key_event", "kind":"presence_away_confirmed",
                "vin":"VIN", "time":"TIME", "error":""
            })
        );
        let state = Notification::PhoneKeyState {
            active: true,
            link: PhoneKeyLink::BluetoothOff,
            status: "STATUS".into(),
            generation: 42,
        };
        assert_eq!(
            serde_json::to_value(state).unwrap(),
            json!({
                "type":"phone_key_state", "active":true, "link":"bluetooth-off",
                "status":"STATUS", "generation":42
            })
        );
        assert_eq!(
            serde_json::to_value(Notification::StatusRefreshRequested).unwrap(),
            json!({"type":"status_refresh_requested"})
        );
    }
}
