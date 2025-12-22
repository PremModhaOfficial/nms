package database

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Repository defines the standard CRUD operations
type Repository[T any] interface {
	List(ctx context.Context) ([]*T, error)
	Get(ctx context.Context, id int64) (*T, error)
	Create(ctx context.Context, entity *T) (*T, error)
	Update(ctx context.Context, id int64, entity *T) (*T, error)
	Delete(ctx context.Context, id int64) error
}

// GormRepository implements Repository using Gorm
type GormRepository[T any] struct {
	db *gorm.DB
}

func NewGormRepository[T any](db *gorm.DB) *GormRepository[T] {
	return &GormRepository[T]{db: db}
}

func (r *GormRepository[T]) List(ctx context.Context) ([]*T, error) {
	var entities []*T
	result := r.db.WithContext(ctx).Find(&entities)
	return entities, result.Error
}

func (r *GormRepository[T]) Get(ctx context.Context, id int64) (*T, error) {
	var entity T
	result := r.db.WithContext(ctx).First(&entity, id)
	if result.Error != nil {
		return nil, result.Error
	}
	return &entity, nil
}

func (r *GormRepository[T]) Create(ctx context.Context, entity *T) (*T, error) {
	result := r.db.WithContext(ctx).Create(entity)
	if result.Error != nil {
		return nil, result.Error
	}
	return entity, nil
}

func (r *GormRepository[T]) Update(ctx context.Context, id int64, entity *T) (*T, error) {
	// Check if exists
	var existing T
	if err := r.db.WithContext(ctx).First(&existing, id).Error; err != nil {
		return nil, err
	}

	result := r.db.WithContext(ctx).Model(&existing).Updates(entity)
	if result.Error != nil {
		return nil, result.Error
	}
	
	// Reload to get full state
	r.db.WithContext(ctx).First(&existing, id)

	return &existing, nil
}

func (r *GormRepository[T]) Delete(ctx context.Context, id int64) error {
	var entity T
	result := r.db.WithContext(ctx).Delete(&entity, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("record not found")
	}
	return nil
}
