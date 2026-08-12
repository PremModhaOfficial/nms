//! Repository layer (Go pkg/database). `Repository<T>` is the DB boundary
//! used by services; DST tests substitute `MemRepository`. The sqlx
//! implementation lives in `sqlx_repo.rs` (delegated to the database agent).

use std::collections::HashMap;
use std::sync::Mutex;

use async_trait::async_trait;

use crate::models::TableNamer;

/// Standard CRUD operations for an entity. Async so sqlx pools fit; DST tests
/// use the in-memory `MemRepository` behind the same trait (deterministic).
#[async_trait]
pub trait Repository<T: TableNamer + Send + Sync>: Send + Sync {
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
    #[error("duplicate key value violates unique constraint")]
    UniqueViolation,
    #[error("foreign key constraint violation")]
    ForeignKeyViolation,
    #[error("invalid filter column: {0}")]
    InvalidFilterColumn(String),
    #[error("database error: {0}")]
    Other(String),
}

/// Id get/set used by MemRepository insert (per-entity impl by the agent).
pub trait HasId {
    fn id(&self) -> i64;
    fn set_id(&mut self, id: i64);
}

/// In-memory, thread-safe, HashMap-backed repository. Used only in tests.
pub struct MemRepository<T> {
    rows: Mutex<HashMap<i64, T>>,
    next_id: Mutex<i64>,
}

impl<T: TableNamer + Clone + Send + Sync + 'static> MemRepository<T> {
    pub fn new() -> Self {
        MemRepository { rows: Mutex::new(HashMap::new()), next_id: Mutex::new(1) }
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

impl<T: TableNamer + Clone + Send + Sync + 'static> Default for MemRepository<T> {
    fn default() -> Self {
        Self::new()
    }
}

/// The sqlx implementation (agent): SqlxRepository<T> + Connect/ConnectRaw
/// pool builders + Repository impls + HasId impls live in `sqlx_repo.rs`.
pub mod sqlx_repo;
