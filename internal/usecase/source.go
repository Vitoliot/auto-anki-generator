package usecase

import (
	"context"
	"errors"
	"io"

	"github.com/example/auto-anki/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrForbidden = errors.New("forbidden")
)

type SourceService struct {
	Sources SourceRepository
	Queue   Queue
	Clock   Clock
	Files   FileStore
}

type CreateSourceInput struct {
	UserID           uuid.UUID
	Title            string
	Type             domain.SourceType
	OriginalFilename string
	Language         *string
	Body             io.Reader
}

func (s SourceService) CreateAndEnqueue(ctx context.Context, in CreateSourceInput) (domain.Source, error) {
	id := uuid.New()
	storagePath, err := s.Files.SaveSource(ctx, id, in.Body)
	if err != nil {
		return domain.Source{}, err
	}

	now := s.Clock.Now()
	src := domain.Source{
		ID:               id,
		UserID:           in.UserID,
		Title:            in.Title,
		Type:             in.Type,
		OriginalFilename: in.OriginalFilename,
		Language:         in.Language,
		Status:           domain.SourceUploaded,
		StoragePath:      storagePath,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.Sources.Create(ctx, src); err != nil {
		return domain.Source{}, err
	}

	if err := s.Queue.EnqueueParseSource(ctx, src.ID); err != nil {
		// best-effort rollback: mark source as error
		_, _ = s.Sources.UpdateStatus(ctx, src.ID, domain.SourceUploaded, domain.SourceError)
		return domain.Source{}, err
	}

	return src, nil
}

func (s SourceService) List(ctx context.Context, userID uuid.UUID) ([]domain.Source, error) {
	return s.Sources.ListByUser(ctx, userID)
}

func (s SourceService) EnsureOwner(ctx context.Context, userID, sourceID uuid.UUID) (domain.Source, error) {
	src, err := s.Sources.FindByID(ctx, sourceID)
	if err != nil {
		return domain.Source{}, err
	}
	if src.UserID != userID {
		return domain.Source{}, ErrForbidden
	}
	return src, nil
}

func (s SourceService) Touch(ctx context.Context, sourceID uuid.UUID) error {
	return s.Sources.Touch(ctx, sourceID, s.Clock.Now())
}
