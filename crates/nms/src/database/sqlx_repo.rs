//! sqlx-backed repository + DB pool builders (Go pkg/database/{db,repository}.go).
//! Implemented by the database agent. Stub compiles; agent replaces bodies.

// The agent fills in: Connect(cfg) -> PgPool, ConnectRaw(cfg, pool_name) ->
// PgPool, SqlxRepository<T> implementing Repository<T> for
// CredentialProfile/Device/DiscoveryProfile, and HasId impls for those three.
