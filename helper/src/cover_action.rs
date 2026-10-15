use std::{fmt, str::FromStr};

use serde::{de, Deserialize, Deserializer, Serialize};

/// Commands that can run from a cover without arguments or a dialog.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, strum::EnumString)]
#[serde(rename_all = "kebab-case")]
#[strum(serialize_all = "kebab-case")]
pub(crate) enum CoverAction {
    TrunkOpen,
    FrunkOpen,
    Lock,
    Unlock,
    FlashLights,
    Honk,
}

/// Parse strings directly from the deserializer's borrowed/transient buffer.
/// Invalid values become None; consume containers without building a JSON tree.
pub(crate) fn deserialize_optional<'de, D>(deserializer: D) -> Result<Option<CoverAction>, D::Error>
where
    D: Deserializer<'de>,
{
    struct OptionalAction;

    impl<'de> de::Visitor<'de> for OptionalAction {
        type Value = Option<CoverAction>;

        fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
            formatter.write_str("a cover action or an ignored invalid value")
        }

        fn visit_str<E: de::Error>(self, value: &str) -> Result<Self::Value, E> {
            Ok(CoverAction::from_str(value).ok())
        }

        fn visit_unit<E: de::Error>(self) -> Result<Self::Value, E> {
            Ok(None)
        }

        fn visit_bool<E: de::Error>(self, _: bool) -> Result<Self::Value, E> {
            Ok(None)
        }

        fn visit_i64<E: de::Error>(self, _: i64) -> Result<Self::Value, E> {
            Ok(None)
        }

        fn visit_u64<E: de::Error>(self, _: u64) -> Result<Self::Value, E> {
            Ok(None)
        }

        fn visit_f64<E: de::Error>(self, _: f64) -> Result<Self::Value, E> {
            Ok(None)
        }

        fn visit_seq<A: de::SeqAccess<'de>>(self, mut seq: A) -> Result<Self::Value, A::Error> {
            while seq.next_element::<de::IgnoredAny>()?.is_some() {}
            Ok(None)
        }

        fn visit_map<A: de::MapAccess<'de>>(self, mut map: A) -> Result<Self::Value, A::Error> {
            while map
                .next_entry::<de::IgnoredAny, de::IgnoredAny>()?
                .is_some()
            {}
            Ok(None)
        }
    }

    deserializer.deserialize_any(OptionalAction)
}
