package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CardsRepo struct{ Pool *pgxpool.Pool }

// InsertBatch inserts cards in a single transaction.
//
// "Дипломный" апгрейд: вставка идемпотентна для карточек, связанных с generation_job_id,
// за счёт UNIQUE(generation_job_id, content_hash) и UPSERT-стратегии.
func (r CardsRepo) InsertBatch(ctx context.Context, cards []domain.Card) error {
	return r.InsertBatchWithLinks(ctx, cards, nil)
}

func (r CardsRepo) InsertBatchWithLinks(ctx context.Context, cards []domain.Card, links []domain.CardSourceLink) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// idMap maps the "proposed" card ID (what the worker generated) to the actual persisted ID.
	// When UPSERT hits a duplicate, we keep the existing row and return its id.
	idMap := make(map[uuid.UUID]uuid.UUID, len(cards))

	for _, c := range cards {
		actualID, err := upsertCardReturnID(ctx, tx, c)
		if err != nil {
			return err
		}
		idMap[c.ID] = actualID
	}

	if len(links) > 0 {
		remapped := make([]domain.CardSourceLink, 0, len(links))
		for _, l := range links {
			if newID, ok := idMap[l.CardID]; ok {
				l.CardID = newID
			}
			// If the card was deduped to an existing one, we still attach links idempotently.
			remapped = append(remapped, l)
		}
		if err := (CardSourceLinksRepo{Pool: r.Pool}).InsertBatch(ctx, tx, remapped); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func upsertCardReturnID(ctx context.Context, tx pgx.Tx, c domain.Card) (uuid.UUID, error) {
	var id uuid.UUID
	// NOTE:
	// - content_hash is a GENERATED column (see migrations/002_cards_dedup.sql)
	// - UNIQUE(generation_job_id, content_hash) makes retries idempotent
	// - we use a no-op DO UPDATE to be able to RETURN the existing id
	err := tx.QueryRow(ctx, `
		insert into cards (
		  id, card_set_id, generation_job_id, user_id, type, question, answer, extra, status,
		  difficulty, srs_interval, srs_due_at, created_at, updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, now(), now())
		on conflict (generation_job_id, content_hash)
		do update set updated_at = cards.updated_at
		returning id
	`, c.ID, c.CardSetID, c.GenerationJobID, c.UserID, c.Type, c.Question, c.Answer, c.Extra, string(c.Status),
		c.Difficulty, c.SRSInterval, c.SRSDueAt,
	).Scan(&id)
	return id, err
}

func (r CardsRepo) List(ctx context.Context, f usecase.CardFilter) ([]domain.Card, error) {
	where := []string{"user_id=$1"}
	args := []any{f.UserID}
	argN := 2

	if f.JobID != nil {
		where = append(where, fmt.Sprintf("generation_job_id=$%d", argN))
		args = append(args, *f.JobID)
		argN++
	}
	if f.CardSetID != nil {
		where = append(where, fmt.Sprintf("card_set_id=$%d", argN))
		args = append(args, *f.CardSetID)
		argN++
	}
	if f.Status != nil {
		where = append(where, fmt.Sprintf("status=$%d", argN))
		args = append(args, string(*f.Status))
		argN++
	}

	args = append(args, f.Limit, f.Offset)
	limitPos := argN
	offsetPos := argN + 1

	q := fmt.Sprintf(`
		select id, card_set_id, generation_job_id, user_id, type, question, answer, extra, status,
		       difficulty, srs_interval, srs_due_at, created_at, updated_at
		from cards
		where %s
		order by created_at desc
		limit $%d offset $%d
	`, strings.Join(where, " and "), limitPos, offsetPos)

	rows, err := r.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Card
	for rows.Next() {
		var c domain.Card
		var st string
		if err := rows.Scan(&c.ID, &c.CardSetID, &c.GenerationJobID, &c.UserID, &c.Type, &c.Question, &c.Answer, &c.Extra, &st,
			&c.Difficulty, &c.SRSInterval, &c.SRSDueAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Status = domain.CardStatus(st)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r CardsRepo) Update(ctx context.Context, id, userID uuid.UUID, patch usecase.CardPatch) (bool, error) {
	sets := []string{}
	args := []any{id, userID}
	argN := 3

	if patch.Status != nil {
		sets = append(sets, fmt.Sprintf("status=$%d", argN))
		args = append(args, string(*patch.Status))
		argN++
	}
	if patch.Question != nil {
		sets = append(sets, fmt.Sprintf("question=$%d", argN))
		args = append(args, *patch.Question)
		argN++
	}
	if patch.Answer != nil {
		sets = append(sets, fmt.Sprintf("answer=$%d", argN))
		args = append(args, *patch.Answer)
		argN++
	}
	if patch.Extra != nil {
		sets = append(sets, fmt.Sprintf("extra=$%d", argN))
		args = append(args, *patch.Extra)
		argN++
	}
	if len(sets) == 0 {
		return false, nil
	}

	q := fmt.Sprintf(`update cards set %s, updated_at=now() where id=$1 and user_id=$2`, strings.Join(sets, ", "))
	ct, err := r.Pool.Exec(ctx, q, args...)
	return ct.RowsAffected() == 1, err
}

func (r CardsRepo) CountApprovedByJob(ctx context.Context, jobID uuid.UUID) (int, error) {
	row := r.Pool.QueryRow(ctx, `select count(*) from cards where generation_job_id=$1 and status='approved'`, jobID)
	var n int
	return n, row.Scan(&n)
}

var _ usecase.CardRepository = CardsRepo{}
