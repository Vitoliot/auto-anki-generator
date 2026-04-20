package usecase

import (
	"context"
	"errors"

	"github.com/example/auto-anki/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrSourceNotIndexed = errors.New("source is not indexed yet")
)

type GenerationService struct {
	Sources  SourceRepository
	CardSets CardSetRepository
	Jobs     GenerationJobRepository
	Queue    Queue
	Clock    Clock
}

type CreateGenerationJobInput struct {
	UserID         uuid.UUID
	SourceID       uuid.UUID
	CardSetID      *uuid.UUID
	CardType       domain.CardType
	MaxCards       int
	TargetLanguage *string
	ModelName      *string
	Temperature    *float64
	TopK           *int
	Seed           *int
}

func (s GenerationService) CreateAndEnqueue(ctx context.Context, in CreateGenerationJobInput) (domain.GenerationJob, error) {
	src, err := s.Sources.FindByID(ctx, in.SourceID)
	if err != nil {
		return domain.GenerationJob{}, err
	}
	if src.UserID != in.UserID {
		return domain.GenerationJob{}, ErrForbidden
	}
	if src.Status != domain.SourceIndexed {
		return domain.GenerationJob{}, ErrSourceNotIndexed
	}

	// If cardSet not provided — create one as "generated".
	var cardSetID *uuid.UUID
	if in.CardSetID != nil {
		cs, err := s.CardSets.FindByID(ctx, *in.CardSetID)
		if err != nil {
			return domain.GenerationJob{}, err
		}
		if cs.UserID != in.UserID {
			return domain.GenerationJob{}, ErrForbidden
		}
		cardSetID = in.CardSetID
	} else {
		newCS := domain.CardSet{
			ID:        uuid.New(),
			UserID:    in.UserID,
			Title:     "Generated deck",
			Origin:    domain.OriginGenerated,
			CreatedAt: s.Clock.Now(),
			UpdatedAt: s.Clock.Now(),
		}
		if err := s.CardSets.Create(ctx, newCS); err != nil {
			return domain.GenerationJob{}, err
		}
		cardSetID = &newCS.ID
	}

	j := domain.GenerationJob{
		ID:          uuid.New(),
		UserID:      in.UserID,
		SourceID:    in.SourceID,
		CardSetID:   cardSetID,
		Status:      domain.JobPending,
		RequestedAt: s.Clock.Now(),
		CardType:    in.CardType,
		MaxCards:    in.MaxCards,
		TargetLang:  in.TargetLanguage,
		ModelName:   in.ModelName,
		Temperature: in.Temperature,
		TopK:        in.TopK,
		Seed:        in.Seed,
	}

	if err := s.Jobs.Create(ctx, j); err != nil {
		return domain.GenerationJob{}, err
	}
	if err := s.Queue.EnqueueGenerateCards(ctx, j.ID); err != nil {
		_, _ = s.Jobs.MarkFinished(ctx, j.ID, domain.JobFailed, s.Clock.Now(), 0, 0, 0, nil)
		return domain.GenerationJob{}, err
	}

	return j, nil
}

func (s GenerationService) Get(ctx context.Context, userID, jobID uuid.UUID) (domain.GenerationJob, error) {
	j, err := s.Jobs.FindByID(ctx, jobID)
	if err != nil {
		return domain.GenerationJob{}, err
	}
	if j.UserID != userID {
		return domain.GenerationJob{}, ErrForbidden
	}
	return j, nil
}
