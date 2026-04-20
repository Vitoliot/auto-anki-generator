package usecase

import (
	"context"

	"github.com/example/auto-anki/internal/domain"
	"github.com/google/uuid"
)

type CardService struct {
	Cards CardRepository
}

func (s CardService) List(ctx context.Context, userID uuid.UUID, jobID, cardSetID *uuid.UUID, status *domain.CardStatus, limit, offset int) ([]domain.Card, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.Cards.List(ctx, CardFilter{
		UserID:    userID,
		JobID:     jobID,
		CardSetID: cardSetID,
		Status:    status,
		Limit:     limit,
		Offset:    offset,
	})
}

func (s CardService) Patch(ctx context.Context, userID, cardID uuid.UUID, patch CardPatch) (bool, error) {
	return s.Cards.Update(ctx, cardID, userID, patch)
}
