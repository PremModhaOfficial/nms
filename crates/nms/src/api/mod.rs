//! HTTP API layer (Go pkg/api) — axum router, JWT auth, encryption bridge,
//! middleware, provisioning and traces handlers.

pub mod jwt;
pub mod middleware;
pub mod provisioning;
pub mod response;
pub mod routes;
pub mod traces;

/// Shared classified error type (Go: classifyError mapping).
#[derive(Debug, thiserror::Error, Clone)]
pub enum ApiError {
    #[error("{0}")]
    NotFound(String),
    #[error("{0}")]
    Conflict(String),
    #[error("{0}")]
    BadRequest(String),
    #[error("internal server error")]
    Internal,
}

impl ApiError {
    /// HTTP status code.
    pub fn status(&self) -> u16 {
        match self {
            ApiError::NotFound(_) => 404,
            ApiError::Conflict(_) => 409,
            ApiError::BadRequest(_) => 400,
            ApiError::Internal => 500,
        }
    }
}

impl From<crate::database::DbError> for ApiError {
    fn from(e: crate::database::DbError) -> Self {
        match e {
            crate::database::DbError::NotFound => ApiError::NotFound("record not found".into()),
            crate::database::DbError::UniqueViolation => ApiError::Conflict("record already exists".into()),
            crate::database::DbError::ForeignKeyViolation => ApiError::Conflict("record is in use by another resource".into()),
            crate::database::DbError::InvalidFilterColumn(msg) => ApiError::BadRequest(msg),
            crate::database::DbError::Other(_) => ApiError::Internal,
        }
    }
}
