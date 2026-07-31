package persistence

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUniqueViolation(t *testing.T) {
	// pgconn-style PgError with SQLSTATE 23505 (from sqlx over pgx stdlib).
	pgErr := &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "devices_ip_address_port_key"`}
	if !isUniqueViolation(pgErr) {
		t.Fatal("PgError 23505 not recognized as unique violation")
	}
	// Wrapped PgError must still be detected via errors.As.
	if !isUniqueViolation(errors.Join(errors.New("outer"), pgErr)) {
		t.Fatal("wrapped PgError 23505 not recognized")
	}
	other := &pgconn.PgError{Code: "22001", Message: "value too long"}
	if isUniqueViolation(other) {
		t.Fatal("non-unique PgError misclassified")
	}
	if isUniqueViolation(errors.New("duplicate key value violates unique constraint")) {
		t.Fatal("plain error should not match (brittle string matching removed)")
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(pgx.ErrNoRows) {
		t.Fatal("pgx.ErrNoRows not recognized")
	}
	if !isNotFound(errors.Join(errors.New("wrapper"), pgx.ErrNoRows)) {
		t.Fatal("wrapped pgx.ErrNoRows not recognized")
	}
	if isNotFound(errors.New("connection refused")) {
		t.Fatal("real DB error misclassified as not-found")
	}
}

func TestSafeContainsPanic(t *testing.T) {
	w := &EntityService{}
	// A panicking handler must be contained, not kill the caller.
	w.safe("test-panic", func() { panic("boom") })
	// The loop must remain usable.
	w.safe("test-ok", func() {})
}
