//! Export an Automagic import bundle using only the app's Documents permission.
//! Automagic owns merging its private configuration and reloading its daemon.
use std::fs;
use std::io;
use std::path::{Path, PathBuf};

const FILES: [(&str, &str); 4] = [
    (
        "data_sources.json",
        include_str!("../../docs/automagic-hotspot/data_sources.json"),
    ),
    (
        "actions.json",
        include_str!("../../docs/automagic-hotspot/actions.json"),
    ),
    (
        "flows.json",
        include_str!("../../docs/automagic-hotspot/flows.json"),
    ),
    (
        "value_maps.json",
        include_str!("../../docs/automagic-hotspot/value_maps.json"),
    ),
];

pub(crate) fn export(documents: &Path) -> io::Result<PathBuf> {
    if !documents.is_absolute() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "Documents directory is unavailable",
        ));
    }
    fs::create_dir_all(documents)?;
    // A fresh folder preserves prior exports, including edits. Publish the
    // complete bundle with one rename; any failure cleans up the staging folder.
    let staging = tempfile::Builder::new()
        .prefix(".ElectricEel-Automagic-")
        .tempdir_in(documents)?;
    for (name, content) in FILES {
        fs::write(staging.path().join(name), content)?;
    }
    let name = staging.path().file_name().unwrap().to_string_lossy();
    let destination = documents.join(name.trim_start_matches('.'));
    fs::rename(staging.path(), &destination)?;
    Ok(destination)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::Value;

    #[test]
    fn exports_complete_imports_without_overwriting_previous_edits() {
        let documents = tempfile::tempdir().unwrap();
        let first = export(documents.path()).unwrap();
        for (file, _) in FILES {
            let json: Value = serde_json::from_slice(&fs::read(first.join(file)).unwrap()).unwrap();
            assert_eq!(json["version"], 1);
            assert!(json.get("data").is_some());
        }
        fs::write(first.join("flows.json"), "user edited this export").unwrap();
        let second = export(documents.path()).unwrap();
        assert_ne!(first, second);
        assert_eq!(
            fs::read_to_string(first.join("flows.json")).unwrap(),
            "user edited this export"
        );
        assert_eq!(fs::read_dir(documents.path()).unwrap().count(), 2);
    }

    #[test]
    fn rejects_missing_documents_location_and_unwritable_destination() {
        assert!(export(Path::new("")).is_err());
        let dir = tempfile::tempdir().unwrap();
        let file = dir.path().join("not-a-directory");
        fs::write(&file, "keep").unwrap();
        assert!(export(&file).is_err());
        assert_eq!(fs::read_to_string(file).unwrap(), "keep");
    }
}
