//! Repository layer (Go pkg/database). `Repository<T>` is the DB boundary
//! used by services; DST tests substitute `MemRepository`. The sqlx
//! implementation lives in `sqlx_repo.rs` (delegated to the database agent).

use std::collections::HashMap;
use std::sync::Mutex;

use async_trait::async_trait;
use serde::Serialize;
use sqlx::FromRow;

use crate::models::TableNamer;

/// Standard CRUD operations for an entity. Async so sqlx pools fit; DST tests
/// use the in-memory `MemRepository` behind the same trait (deterministic).
#[async_trait]
pub trait Repository<T: TableNamer + FromRow + Send + Sync>: Send + Sync {
    async fn list(&self) -> Result<Vec<T>, DbError>;
    async fn get(&self, id: i64) -> Result<Option<T>, DbError>;
    /// Get by validated filter columns (allowlist enforced — SQL injection).
    async fn get_by_fields(&self, filters: &HashMap<String, DbValue>) -> Result<Option<T>, DbError>;
    async fn create(&self, entity: &T) -> Result<T, DbError>;
    async fn update(&self, id: i64, entity: &T) -> Result<T, DbError>;
    async fn delete(&self, id: i64) -> Result<(), DbError>;
}

/// Filter parameter value (avoids generics in the trait).
#[derive(Debug, Clone)]
pub enum DbValue {
    Int(i64),
    Str(String),
    Bool(bool),
}

/// Repository error classification (Go: sql.ErrNoRows / pgconn codes).
#[derive(Debug, thiserror::Error)]
pub enum DbError {
    #[error("record not found")]
    NotFound,
    #[error("unique constraint violation")]
    UniqueViolation,
    #[error("foreign key violation")]
    ForeignKeyViolation,
    #[error("invalid filter column: {0}")]
    InvalidFilterColumn(String),
    #[error("database error: {0}")]
    Other(String),
}

/// Build a `(column = ?, ...)` filter clause with $n placeholders. Columns are
/// validated against the allowlist before interpolation (tiger: SQL injection
/// is rejected at the boundary, mirroring Go buildFilterClause).
pub fn build_filter_clause<T: TableNamer>(
    filters: &HashMap<String, DbValue>,
    valid_columns: &[&str],
) -> Result<(String, Vec<DbValue>), DbError> {
    if filters.is_empty() {
        return Err(DbError::InvalidFilterColumn(
            "GetByFields requires at least one filter".into(),
        ));
    }
    let mut conditions = Vec::with_capacity(filters.len());
    let mut args = Vec::with_capacity(filters.len());
    let mut idx = 1usize;
    for (col, val) in filters {
        if !valid_columns.contains(&col.as_str()) {
            return Err(DbError::InvalidFilterColumn(col.clone()));
        }
        conditions.push(format!("{col} = ${idx}"));
        args.push(val.clone());
        idx += 1;
    }
    Ok((conditions.join(" AND "), args))
}

/// Column names of an entity struct via the db tag. Implemented per entity by
/// the database agent (Rust has no reflection — ponytail: a macro/impl per
/// entity, matching the explicit FromRow derive).
pub trait ColumnNames {
    /// All db columns, in declaration order, excluding id/created_at/updated_at
    /// for inserts and the cache-only fields.
    fn insert_columns() -> &'static [&'static str];
    /// Columns settable on update (update:"omitempty" semantics live in the
    /// per-entity update builder).
    fn update_columns() -> &'static [&'static str];
    /// Columns allowed in filter clauses (GetByFields allowlist).
    fn filter_columns() -> &'static [&'static str];
}

// ─────────────────────────────────────────────────────────────────────────────
// In-memory repository for DST tests (deterministic, no DB).
// ─────────────────────────────────────────────────────────────────────────────

/// In-memory, thread-safe, HashMap-backed repository. Used only in tests.
pub struct MemRepository<T> {
    rows: Mutex<HashMap<i64, T>>,
    next_id: Mutex<i64>,
    // Keep serde/sqlx imports used (FromRow bound enforced by trait users).
    _marker: std::marker::PhantomData<fn() -> T>,
}

impl<T: TableNamer + FromRow + Clone + Send + Sync + Serialize + 'static> MemRepository<T> {
    pub fn new() -> Self {
        MemRepository {
            rows: Mutex::new(HashMap::new()),
            next_id: Mutex::new(1),
            _marker: std::marker::PhantomData,
        }
    }

    /// Insert with an auto-incrementing id (mirrors DB RETURNING *).
    pub fn insert(&self, mut entity: T) -> i64
    where
        T: HasId,
    {
        let mut next = self.next_id.lock().expect("memrepo poisoned");
        let id = *next;
        *next += 1;
        entity.set_id(id);
        let mut rows = self.rows.lock().expect("memrepo poisoned");
        rows.insert(id, entity);
        id
    }

    pub fn snapshot(&self) -> Vec<(i64, T)> {
        let rows = self.rows.lock().expect("memrepo poisoned");
        let mut v: Vec<(i64, T)> = rows.iter().map(|(k, v)| (*k, v.clone())).collect();
        v.sort_by_key(|(k, _)| *k);
        v
    }

    pub fn len(&self) -> usize {
        self.rows.lock().expect("memrepo poisoned").len()
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }
}

impl<T> Default for MemRepository<T>
where
    T: TableNamer + FromRow + Clone + Send + Sync + Serialize + 'static,
{
    fn default() -> Self {
        Self::new()
    }
}

/// Id get/set for MemRepository insert (per-entity impl by the agent).
pub trait HasId {
    fn id(&self) -> i64;
    fn set_id(&mut self, id: i64);
}

/// The sqlx implementation (agent): SqlxRepository<T> + Connect/ConnectRaw
/// pool builders + Repository impls + ColumnNames + HasId impls live in
/// `sqlx_repo.rs`, wired here via `pub mod sqlx_repo`.
pub mod sqlx_repo;
