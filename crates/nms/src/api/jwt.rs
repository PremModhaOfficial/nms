//! JWT auth (Go pkg/api/jwtAuth.go). Implemented by the api agent.

use crate::config::Config;

pub struct JwtAuth {
    _secret: Vec<u8>,
    _admin_username: String,
    _admin_hash: String,
    _expiry_hours: i32,
}

impl JwtAuth {
    pub fn new(cfg: &Config) -> Result<JwtAuth, String> {
        assert!(!cfg.jwt_secret.trim().is_empty(), "JWT_SECRET must not be empty");
        assert!(!cfg.admin_hash.trim().is_empty(), "NMS_ADMIN_HASH must not be empty");
        assert!(cfg.session_duration_hours >= 1, "SESSION_DURATION_HOURS must be >= 1");
        Ok(JwtAuth {
            _secret: cfg.jwt_secret.as_bytes().to_vec(),
            _admin_username: cfg.admin_user.clone(),
            _admin_hash: cfg.admin_hash.clone(),
            _expiry_hours: cfg.session_duration_hours,
        })
    }
}
