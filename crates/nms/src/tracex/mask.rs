//! Sensitive-key redaction for trace capture (Go tracex MaskJSON/BodyEvent).
//! Credential payloads never leave the server.

use serde_json::Value;

/// JSON object keys whose values are redacted. Case-insensitive matching
/// covers "Authorization" headers and the like.
pub const SENSITIVE_KEYS: [&str; 5] = ["payload", "password", "secret", "token", "authorization"];

/// Recursively replace the value of every sensitive key with "[HIDDEN]".
/// Invalid JSON is returned unchanged; MaskJSON never fails.
pub fn mask_json(bytes: &[u8]) -> Vec<u8> {
    let Ok(mut v) = serde_json::from_slice::<Value>(bytes) else {
        return bytes.to_vec();
    };
    mask_json_value(&mut v);
    serde_json::to_vec(&v).unwrap_or_else(|_| bytes.to_vec())
}

fn mask_json_value(v: &mut Value) {
    match v {
        Value::Object(map) => {
            let keys: Vec<String> = map.keys().cloned().collect();
            for k in keys {
                if is_sensitive_key(&k) {
                    map.insert(k, Value::String("[HIDDEN]".into()));
                } else if let Some(val) = map.get_mut(&k) {
                    mask_json_value(val);
                }
            }
        }
        Value::Array(arr) => {
            for item in arr.iter_mut() {
                mask_json_value(item);
            }
        }
        _ => {}
    }
}

fn is_sensitive_key(k: &str) -> bool {
    SENSITIVE_KEYS.iter().any(|name| k.eq_ignore_ascii_case(name))
}

/// Masked, 64 KiB-capped copy of a body for span event capture.
pub fn body_event_body(body: &[u8]) -> String {
    let masked = mask_json(body);
    let capped = if masked.len() > super::MAX_BODY_ATTR_BYTES {
        &masked[..super::MAX_BODY_ATTR_BYTES]
    } else {
        &masked[..]
    };
    String::from_utf8_lossy(capped).into_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn masks_nested_and_array_keys() {
        let input = br#"{"ok":true,"data":{"password":"hunter2","token":"abc","nested":{"secret":"x","Authorization":"Bearer y","items":[{"token":"z"}]}}}"#;
        let out = mask_json(input);
        let v: Value = serde_json::from_slice(&out).unwrap();
        assert_eq!(v["ok"], Value::Bool(true));
        assert_eq!(v["data"]["password"], Value::String("[HIDDEN]".into()));
        assert_eq!(v["data"]["token"], Value::String("[HIDDEN]".into()));
        assert_eq!(v["data"]["nested"]["secret"], Value::String("[HIDDEN]".into()));
        assert_eq!(v["data"]["nested"]["Authorization"], Value::String("[HIDDEN]".into()));
        assert_eq!(v["data"]["nested"]["items"][0]["token"], Value::String("[HIDDEN]".into()));
    }

    #[test]
    fn payload_object_collapses() {
        let out = mask_json(br#"{"payload":{"password":"x"}}"#);
        assert_eq!(String::from_utf8(out).unwrap(), r#"{"payload":"[HIDDEN]"}"#);
    }

    #[test]
    fn invalid_json_passthrough() {
        let bad = b"{not json";
        assert_eq!(mask_json(bad), bad);
        assert_eq!(mask_json(b""), b"");
    }
}
