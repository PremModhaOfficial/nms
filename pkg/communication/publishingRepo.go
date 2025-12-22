package communication

import (
	"context"
	"nms/pkg/database"
	"nms/pkg/models"
)

// PublishingRepo wraps a repository and publishes events on mutations.
type PublishingRepo[T any] struct {
	inner   database.Repository[T]
	eventCh chan<- models.Event
}

// NewPublishingRepo creates a wrapper that publishes events on Create/Update/Delete.
func NewPublishingRepo[T any](inner database.Repository[T], eventCh chan<- models.Event) *PublishingRepo[T] {
	return &PublishingRepo[T]{inner: inner, eventCh: eventCh}
}

func (r *PublishingRepo[T]) Create(ctx context.Context, entity *T) (*T, error) {
	result, err := r.inner.Create(ctx, entity)
	if err == nil {
		r.eventCh <- models.Event{Type: models.EventCreate, Payload: result}
	}
	return result, err
}

func (r *PublishingRepo[T]) Update(ctx context.Context, id int64, entity *T) (*T, error) {
	result, err := r.inner.Update(ctx, id, entity)
	if err == nil {
		r.eventCh <- models.Event{Type: models.EventUpdate, Payload: result}
	}
	return result, err
}

func (r *PublishingRepo[T]) Delete(ctx context.Context, id int64) error {
	entity, _ := r.inner.Get(ctx, id)
	err := r.inner.Delete(ctx, id)
	if err == nil && entity != nil {
		r.eventCh <- models.Event{Type: models.EventDelete, Payload: entity}
	}
	return err
}

// List passes through to the inner repository (no event).
func (r *PublishingRepo[T]) List(ctx context.Context) ([]*T, error) {
	return r.inner.List(ctx)
}

// Get passes through to the inner repository (no event).
func (r *PublishingRepo[T]) Get(ctx context.Context, id int64) (*T, error) {
	return r.inner.Get(ctx, id)
}
