//! Middleware: SecurityHeaders, MaxBodyBytes, TraceMiddleware (Go pkg/api/middleware.go).
//! Implemented by the api agent.

pub fn security_headers() -> tower::layer::util::Identity {
    tower::layer::util::Identity::new()
}

pub fn max_body_bytes(_limit: usize) -> tower::layer::util::Identity {
    tower::layer::util::Identity::new()
}

pub fn trace_middleware(_tracer: std::sync::Arc<crate::tracex::Tracer>) -> tower::layer::util::Identity {
    tower::layer::util::Identity::new()
}
