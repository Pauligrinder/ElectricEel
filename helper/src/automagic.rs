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
    let name = staging
        .path()
        .file_name()
        .map(|name| name.to_string_lossy().trim_start_matches('.').to_string())
        .ok_or_else(|| io::Error::other("staging directory has no name"))?;
    let destination = documents.join(name);
    fs::rename(staging.path(), &destination)?;
    // Published: the staging path no longer exists, so disarm its cleanup.
    let _ = staging.keep();
    prune_exports(documents, &destination);
    Ok(destination)
}

// Only prune untouched generated bundles. Edited exports belong to the user.
fn prune_exports(documents: &Path, current: &Path) {
    let Ok(entries) = fs::read_dir(documents) else {
        return;
    };
    let mut exports: Vec<_> = entries
        .filter_map(Result::ok)
        .filter_map(|entry| {
            let path = entry.path();
            if !entry.file_type().ok()?.is_dir()
                || !entry
                    .file_name()
                    .to_string_lossy()
                    .starts_with("ElectricEel-Automagic-")
            {
                return None;
            }
            let files: Vec<_> = fs::read_dir(&path).ok()?.collect();
            if files.len() != FILES.len()
                || !FILES.iter().all(|(name, content)| {
                    let file = path.join(name);
                    fs::symlink_metadata(&file).is_ok_and(|m| m.file_type().is_file())
                        && fs::read(&file).is_ok_and(|bytes| bytes == content.as_bytes())
                })
            {
                return None;
            }
            Some((entry.metadata().ok()?.modified().ok()?, path))
        })
        .collect();
    exports.sort();
    let mut excess = exports.len().saturating_sub(5);
    for (_, path) in exports {
        if excess == 0 {
            break;
        }
        if path != current && fs::remove_dir_all(path).is_ok() {
            excess -= 1;
        }
    }
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
    fn caps_untouched_exports_but_preserves_edits_and_extra_files() {
        let documents = tempfile::tempdir().unwrap();
        let edited = export(documents.path()).unwrap();
        fs::write(edited.join("flows.json"), "user edits").unwrap();
        let extra = export(documents.path()).unwrap();
        fs::write(extra.join("notes.txt"), "user notes").unwrap();
        let mut latest = PathBuf::new();
        for _ in 0..8 {
            latest = export(documents.path()).unwrap();
        }
        assert!(latest.exists());
        assert_eq!(fs::read_dir(documents.path()).unwrap().count(), 7);
        assert_eq!(
            fs::read_to_string(edited.join("flows.json")).unwrap(),
            "user edits"
        );
        assert_eq!(
            fs::read_to_string(extra.join("notes.txt")).unwrap(),
            "user notes"
        );
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
