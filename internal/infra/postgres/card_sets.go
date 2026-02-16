package postgres

import (
	"context"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CardSetsRepo struct{ Pool *pgxpool.Pool }

func (r CardSetsRepo) Create(ctx context.Context, cs domain.CardSet) error {
	_, err := r.Pool.Exec(ctx, `
		insert into card_sets (id, user_id, title, description, origin, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7)
	`, cs.ID, cs.UserID, cs.Title, cs.Description, string(cs.Origin), cs.CreatedAt, cs.UpdatedAt)
	return err
}

func (r CardSetsRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.CardSet, error) {
	row := r.Pool.QueryRow(ctx, `
		select id, user_id, title, description, origin, created_at, updated_at
		from card_sets where id=$1
	`, id)
	var cs domain.CardSet
	var origin string
	var desc *string
	var created, updated time.Time
	if err := row.Scan(&cs.ID, &cs.UserID, &cs.Title, &desc, &origin, &created, &updated); err != nil {
		return domain.CardSet{}, err
	}
	cs.Description = desc
	cs.Origin = domain.CardSetOrigin(origin)
	cs.CreatedAt = created
	cs.UpdatedAt = updated
	return cs, nil
}

var _ usecase.CardSetRepository = CardSetsRepo{}
