package database

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"nms/pkg/models"

	"github.com/jmoiron/sqlx"
)

// Repository defines the standard CRUD operations
type Repository[T models.TableNamer] interface {
	List(ctx context.Context) ([]*T, error)
	Get(ctx context.Context, id int64) (*T, error)
	GetByFields(ctx context.Context, filters map[string]any) (*T, error)
	Create(ctx context.Context, entity *T) (*T, error)
	Update(ctx context.Context, id int64, entity *T) (*T, error)
	Delete(ctx context.Context, id int64) error
}

// SqlxRepository implements Repository using sqlx
type SqlxRepository[T models.TableNamer] struct {
	db *sqlx.DB
}

func NewSqlxRepository[T models.TableNamer](db *sqlx.DB) *SqlxRepository[T] {
	return &SqlxRepository[T]{db: db}
}

func (r *SqlxRepository[T]) tableName() string {
	var zero T
	return zero.TableName()
}

// DB returns the underlying database connection for specialized queries
func (r *SqlxRepository[T]) DB() *sqlx.DB {
	return r.db
}

func (r *SqlxRepository[T]) List(ctx context.Context) ([]*T, error) {
	var entities []*T
	query := fmt.Sprintf("SELECT * FROM %s", r.tableName())
	err := r.db.SelectContext(ctx, &entities, query)
	return entities, err
}

func (r *SqlxRepository[T]) Get(ctx context.Context, id int64) (*T, error) {
	var entity T
	query := fmt.Sprintf("SELECT * FROM %s WHERE id = $1", r.tableName())
	err := r.db.GetContext(ctx, &entity, query, id)
	if err != nil {
		return nil, err
	}
	return &entity, nil
}

func (r *SqlxRepository[T]) GetByFields(ctx context.Context, filters map[string]any) (*T, error) {
	var entity T

	// Build WHERE clause dynamically
	var conditions []string
	var args []any
	i := 1
	for col, val := range filters {
		conditions = append(conditions, fmt.Sprintf("%s = $%d", col, i))
		args = append(args, val)
		i++
	}

	query := fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT 1",
		r.tableName(), strings.Join(conditions, " AND "))
	err := r.db.GetContext(ctx, &entity, query, args...)
	if err != nil {
		return nil, err
	}
	return &entity, nil
}

func (r *SqlxRepository[T]) Create(ctx context.Context, entity *T) (*T, error) {
	cols, placeholders, vals := buildInsertParts(entity)
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		r.tableName(), cols, placeholders)

	rows, err := r.db.QueryxContext(ctx, query, vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if rows.Next() {
		if err := rows.StructScan(entity); err != nil {
			return nil, err
		}
	}
	return entity, nil
}

func (r *SqlxRepository[T]) Update(ctx context.Context, id int64, entity *T) (*T, error) {
	setParts, vals := buildUpdateParts(entity)
	vals = append(vals, id)
	query := fmt.Sprintf("UPDATE %s SET %s, updated_at = NOW() WHERE id = $%d RETURNING *",
		r.tableName(), setParts, len(vals))

	rows, err := r.db.QueryxContext(ctx, query, vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if rows.Next() {
		if err := rows.StructScan(entity); err != nil {
			return nil, err
		}
	}
	return entity, nil
}

func (r *SqlxRepository[T]) Delete(ctx context.Context, id int64) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1", r.tableName())
	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("record not found")
	}
	return nil
}

// buildInsertParts returns column names, placeholders, and values for INSERT
// Skips id, created_at, updated_at (auto-generated) and fields marked db:"-"
func buildInsertParts(entity any) (cols string, placeholders string, vals []any) {
	v := reflect.ValueOf(entity).Elem()
	t := v.Type()

	var colList []string
	var phList []string
	idx := 1

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		dbTag := field.Tag.Get("db")

		// Skip auto-generated and ignored fields
		if dbTag == "" || dbTag == "-" || dbTag == "id" || dbTag == "created_at" || dbTag == "updated_at" {
			continue
		}

		colList = append(colList, dbTag)
		phList = append(phList, fmt.Sprintf("$%d", idx))
		vals = append(vals, v.Field(i).Interface())
		idx++
	}

	return strings.Join(colList, ", "), strings.Join(phList, ", "), vals
}

// buildUpdateParts returns SET clause and values for UPDATE
// Skips id, created_at, updated_at and fields marked db:"-"
func buildUpdateParts(entity any) (setParts string, vals []any) {
	v := reflect.ValueOf(entity).Elem()
	t := v.Type()

	var parts []string
	idx := 1

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		dbTag := field.Tag.Get("db")

		// Skip auto-generated and ignored fields
		if dbTag == "" || dbTag == "-" || dbTag == "id" || dbTag == "created_at" || dbTag == "updated_at" {
			continue
		}

		// Skip zero time values (optional fields not provided)
		if field.Type == reflect.TypeOf(time.Time{}) && v.Field(i).Interface().(time.Time).IsZero() {
			continue
		}

		parts = append(parts, fmt.Sprintf("%s = $%d", dbTag, idx))
		vals = append(vals, v.Field(i).Interface())
		idx++
	}

	return strings.Join(parts, ", "), vals
}
