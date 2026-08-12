//! JSON response helpers + capture writer (Go pkg/api/response.go).
//! Implemented by the api agent.

use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};

/// Structured error response (Go respondError).
pub fn respond_error(code: u16, message: &str) -> Response {
    let body = serde_json::json!({ "error": { "message": message, "status": code } });
    (StatusCode::from_u16(code).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR), axum::Json(body)).into_response()
}

/// JSON response with the given status (Go writeJSON).
pub fn write_json(code: u16, value: &serde_json::Value) -> Response {
    (StatusCode::from_u16(code).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR), axum::Json(value.clone())).into_response()
}
