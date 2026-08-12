//! AES-256-GCM credential encryption (Go pkg/api/encryption.go).
//! Wire format MUST match Go byte-for-byte: 64-hex-char key, 12-byte random
//! nonce, ciphertext = hex(nonce ‖ ct). Existing stored ciphertext stays
//! decryptable with no migration.

use aes_gcm::aead::{Aead, KeyInit};
use rand::Rng;
use aes_gcm::Aes256Gcm;
use serde::de::DeserializeOwned;
use serde::Serialize;

/// Build an AES-256-GCM cipher from a 64-char hex key.
fn new_aead(secret_key: &str) -> Result<Aes256Gcm, String> {
    if secret_key.len() != 64 {
        return Err(format!(
            "encryption key must be 64 hex characters, got {}",
            secret_key.len()
        ));
    }
    let key = hex::decode(secret_key).map_err(|e| format!("invalid hex encryption key: {e}"))?;
    Aes256Gcm::new_from_slice(&key).map_err(|e| format!("invalid AES key: {e}"))
}

/// Encrypt plain with AES-256-GCM and hex-encode the nonce-prefixed ciphertext.
fn encrypt_string(aead: &Aes256Gcm, plain: &str) -> Result<String, String> {
    let mut nonce_bytes = [0u8; 12];
    rand::rng().fill_bytes(&mut nonce_bytes);
    let nonce: aes_gcm::Nonce<aes_gcm::aead::consts::U12> = nonce_bytes.into();
    let ciphertext = aead
        .encrypt(&nonce, plain.as_bytes())
        .map_err(|e| format!("encrypt failed: {e}"))?;
    // nonce-prefixed, hex-encoded (Go layout)
    let mut out = Vec::with_capacity(nonce_bytes.len() + ciphertext.len());
    out.extend_from_slice(&nonce_bytes);
    out.extend_from_slice(&ciphertext);
    Ok(hex::encode(out))
}

/// Decode and decrypt the nonce-prefixed hex ciphertext.
fn decrypt_string(aead: &Aes256Gcm, encoded: &str) -> Result<String, String> {
    let ciphertext =
        hex::decode(encoded).map_err(|e| format!("invalid hex ciphertext: {e}"))?;
    let nonce_size = 12;
    if ciphertext.len() < nonce_size {
        return Err("ciphertext too short".into());
    }
    let (nonce, ct) = ciphertext.split_at(nonce_size);
    let nonce_arr: [u8; 12] = nonce
        .try_into()
        .map_err(|_| "invalid nonce length".to_string())?;
    let nonce: aes_gcm::Nonce<aes_gcm::aead::consts::U12> = nonce_arr.into();
    let plain = aead
        .decrypt(&nonce, ct)
        .map_err(|e| format!("decrypt failed: {e}"))?;
    String::from_utf8(plain).map_err(|e| format!("decrypted payload not UTF-8: {e}"))
}

/// Encrypt the `payload` field of a serializable entity (only CredentialProfile
/// carries one today). Returns a copy with the payload encrypted. Empty
/// payloads are left untouched so partial updates can omit them.
pub fn encrypt_payload<T>(entity: &T, secret_key: &str) -> Result<T, String>
where
    T: Serialize + DeserializeOwned + PayloadHolder,
{
    let aead = new_aead(secret_key)?;
    transform_payload(entity, &aead, encrypt_string)
}

/// Decrypt the `payload` field. Mirrors Go EncryptStruct/DecryptStruct for the
/// CredentialProfile type specifically (generic over a PayloadHolder).
pub fn decrypt_payload<T>(entity: &T, secret_key: &str) -> Result<T, String>
where
    T: Serialize + DeserializeOwned + PayloadHolder,
{
    let aead = new_aead(secret_key)?;
    let mut out = serde_json::from_value::<T>(serde_json::to_value(entity).map_err(|e| e.to_string())?)
        .map_err(|e| e.to_string())?;
    out.set_payload(decrypt_string(&aead, entity.payload())?);
    Ok(out)
}

/// Types that carry an encryptable credential payload.
pub trait PayloadHolder {
    fn payload(&self) -> &str;
    fn set_payload(&mut self, payload: String);
}

impl PayloadHolder for crate::models::CredentialProfile {
    fn payload(&self) -> &str {
        &self.payload
    }
    fn set_payload(&mut self, payload: String) {
        self.payload = payload;
    }
}

/// Generic field transform for the single `payload` field. (Go reflects over
/// gocrypt:"aes" tagged fields; we have exactly one such field today —
/// ponytail: hardcode the one real case, add a derive when a second appears.)
fn transform_payload<T, F>(
    entity: &T,
    aead: &Aes256Gcm,
    f: F,
) -> Result<T, String>
where
    T: Serialize + DeserializeOwned + PayloadHolder,
    F: Fn(&Aes256Gcm, &str) -> Result<String, String>,
{
    let mut out = serde_json::from_value::<T>(serde_json::to_value(entity).map_err(|e| e.to_string())?)
        .map_err(|e| e.to_string())?;
    let payload = entity.payload();
    if payload.is_empty() {
        return Ok(out);
    }
    out.set_payload(f(aead, payload)?);
    Ok(out)
}

/// Decrypt a CredentialProfile and return the raw payload as JSON. On
/// failure, falls back to raw payload when APP_ENV != production and the
/// payload starts with `{` (dev/migration fallback, Go parity).
pub fn decrypt_credential_payload(
    cred: &crate::models::CredentialProfile,
    secret_key: &str,
) -> Result<Option<serde_json::Value>, String> {
    match decrypt_payload(cred, secret_key) {
        Ok(decrypted) => {
            if decrypted.payload.is_empty() {
                Ok(None)
            } else {
                serde_json::from_str(&decrypted.payload)
                    .map(Some)
                    .map_err(|e| format!("decrypted payload is not JSON: {e}"))
            }
        }
        Err(e) => {
            let is_prod = std::env::var("APP_ENV").map(|v| v == "production").unwrap_or(false);
            if !is_prod && !cred.payload.is_empty() && cred.payload.starts_with('{') {
                serde_json::from_str(&cred.payload).map(Some).map_err(|e| e.to_string())
            } else {
                Err(e)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::CredentialProfile;
    use chrono::Utc;

    const KEY: &str = "1234567890123456789012345678901212345678901234567890123456789012";

    fn cred(payload: &str) -> CredentialProfile {
        CredentialProfile {
            id: 1,
            name: "win".into(),
            protocol: "winrm".into(),
            payload: payload.into(),
            created_at: Utc::now(),
            updated_at: Utc::now(),
        }
    }

    #[test]
    fn round_trip() {
        let c = cred(r#"{"user":"u","pass":"p"}"#);
        let enc = encrypt_payload(&c, KEY).unwrap();
        assert_ne!(enc.payload, c.payload, "payload was not encrypted");
        let dec = decrypt_payload(&enc, KEY).unwrap();
        assert_eq!(dec.payload, c.payload, "decrypted payload mismatch");
    }

    #[test]
    fn empty_payload_untouched() {
        let c = cred("");
        let enc = encrypt_payload(&c, KEY).unwrap();
        assert_eq!(enc.payload, "", "empty payload must stay empty");
    }

    #[test]
    fn bad_key_len_rejected() {
        assert!(new_aead("short").is_err(), "short key must be rejected");
    }
}
