//! Rust owns persistent keys. Go receives copied scalar/public-point bytes only.
use p256::{
    elliptic_curve::sec1::ToEncodedPoint,
    pkcs8::{DecodePrivateKey, DecodePublicKey, EncodePublicKey, LineEnding},
    PublicKey, SecretKey,
};
use std::path::Path;

pub(crate) fn load(path: &Path) -> Result<SecretKey, String> {
    let pem =
        zeroize::Zeroizing::new(std::fs::read_to_string(path).map_err(|error| error.to_string())?);
    SecretKey::from_sec1_pem(&pem)
        .or_else(|_| SecretKey::from_pkcs8_pem(&pem))
        .map_err(|error| format!("invalid private key: {error}"))
}

pub(crate) fn public_pem(key: &SecretKey) -> Result<String, String> {
    key.public_key()
        .to_public_key_pem(LineEnding::LF)
        .map_err(|error| error.to_string())
}

pub(crate) fn public_bytes(path: &Path) -> Result<Vec<u8>, String> {
    let data = std::fs::read(path).map_err(|error| error.to_string())?;
    if let Ok(key) = PublicKey::from_sec1_bytes(&data) {
        return Ok(key.to_encoded_point(false).as_bytes().to_vec());
    }
    let text = std::str::from_utf8(&data).map_err(|error| error.to_string())?;
    if let Ok(key) = PublicKey::from_public_key_pem(text) {
        return Ok(key.to_encoded_point(false).as_bytes().to_vec());
    }
    if let Ok(key) = load(path) {
        return Ok(key.public_key().to_encoded_point(false).as_bytes().to_vec());
    }
    let text = text.trim();
    if text.len() == 130 && text.is_ascii() {
        let bytes: Result<Vec<u8>, _> = (0..130)
            .step_by(2)
            .map(|index| u8::from_str_radix(&text[index..index + 2], 16))
            .collect();
        if let Ok(bytes) = bytes {
            if let Ok(key) = PublicKey::from_sec1_bytes(&bytes) {
                return Ok(key.to_encoded_point(false).as_bytes().to_vec());
            }
        }
    }
    Err("invalid public key".into())
}

pub(crate) fn generate(private: &Path, public: &Path, force: bool) -> Result<String, String> {
    let key = if private.exists() && !force {
        load(private)?
    } else {
        let key = SecretKey::random(&mut rand_core::OsRng);
        let pem = key
            .to_sec1_pem(LineEnding::LF)
            .map_err(|error| error.to_string())?;
        crate::config::write_atomic(private, pem.as_bytes()).map_err(|error| error.to_string())?;
        key
    };
    let pem = public_pem(&key)?;
    crate::config::write_atomic(public, pem.as_bytes()).map_err(|error| error.to_string())?;
    Ok(pem.trim().into())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn key_reuse_repairs_public_half_without_rotating_private_identity() {
        let dir = tempfile::tempdir().unwrap();
        let private = dir.path().join("private.pem");
        let public = dir.path().join("public.pem");
        let first = generate(&private, &public, false).unwrap();
        let original = std::fs::read(&private).unwrap();
        std::fs::write(&public, "stale").unwrap();
        assert_eq!(generate(&private, &public, false).unwrap(), first);
        assert_eq!(std::fs::read(&private).unwrap(), original);
        assert_eq!(
            public_bytes(&public).unwrap(),
            load(&private)
                .unwrap()
                .public_key()
                .to_encoded_point(false)
                .as_bytes()
        );
        assert_ne!(generate(&private, &public, true).unwrap(), first);
    }
    #[test]
    fn corrupt_private_key_is_not_silently_replaced() {
        let dir = tempfile::tempdir().unwrap();
        let private = dir.path().join("private.pem");
        let public = dir.path().join("public.pem");
        std::fs::write(&private, "corrupt").unwrap();
        assert!(generate(&private, &public, false).is_err());
        assert_eq!(std::fs::read_to_string(private).unwrap(), "corrupt");
    }
}
