package database

import (
	"context"
	"fmt"

	"nms/pkg/models"

	"gorm.io/gorm"
)

// PreloadingDiscoveryProfileRepo is a GormRepository for DiscoveryProfile
// that automatically preloads the CredentialProfile relation.
type PreloadingDiscoveryProfileRepo struct {
	db *gorm.DB
}

func NewPreloadingDiscoveryProfileRepo(db *gorm.DB) *PreloadingDiscoveryProfileRepo {
	return &PreloadingDiscoveryProfileRepo{db: db}
}

func (r *PreloadingDiscoveryProfileRepo) List(ctx context.Context) ([]*models.DiscoveryProfile, error) {
	var entities []*models.DiscoveryProfile
	result := r.db.WithContext(ctx).Preload("CredentialProfile").Find(&entities)
	return entities, result.Error
}

func (r *PreloadingDiscoveryProfileRepo) Get(ctx context.Context, id int64) (*models.DiscoveryProfile, error) {
	var entity models.DiscoveryProfile
	result := r.db.WithContext(ctx).Preload("CredentialProfile").First(&entity, id)
	if result.Error != nil {
		return nil, result.Error
	}
	return &entity, nil
}

func (r *PreloadingDiscoveryProfileRepo) Create(ctx context.Context, entity *models.DiscoveryProfile) (*models.DiscoveryProfile, error) {
	result := r.db.WithContext(ctx).Create(entity)
	if result.Error != nil {
		return nil, result.Error
	}
	// Reload with preload to get the credential profile
	return r.Get(ctx, entity.ID)
}

func (r *PreloadingDiscoveryProfileRepo) Update(ctx context.Context, id int64, entity *models.DiscoveryProfile) (*models.DiscoveryProfile, error) {
	var existing models.DiscoveryProfile
	if err := r.db.WithContext(ctx).First(&existing, id).Error; err != nil {
		return nil, err
	}

	result := r.db.WithContext(ctx).Model(&existing).Updates(entity)
	if result.Error != nil {
		return nil, result.Error
	}

	// Reload with preload
	return r.Get(ctx, id)
}

func (r *PreloadingDiscoveryProfileRepo) Delete(ctx context.Context, id int64) error {
	var entity models.DiscoveryProfile
	result := r.db.WithContext(ctx).Delete(&entity, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("record not found")
	}
	return nil
}
