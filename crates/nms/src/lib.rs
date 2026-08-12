//! NMS (Network Monitoring System) — Rust port of the Go codebase.
//! See docs/rust-port-wisdom.md for the working wisdom / contracts.

pub mod api;
pub mod config;
pub mod crypto;
pub mod database;
pub mod models;
pub mod plugin;
pub mod plugin_worker;
pub mod services;
pub mod tracex;

/// Shared channel buffer sizes (Go cmd/app/main.go).
pub mod buffers {
    /// High-volume result channels.
    pub const DATA_BUFFER_SIZE: usize = 1000;
    /// Standard event/request channels.
    pub const EVENT_BUFFER_SIZE: usize = 100;
    /// Low-volume control/batch channels.
    pub const CONTROL_BUFFER_SIZE: usize = 50;
}
