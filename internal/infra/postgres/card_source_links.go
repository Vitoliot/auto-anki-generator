package postgres

import (
	"context"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CardSourceLinksRepo struct{ Pool *pgxpool.Pool }

func (r CardSourceLinksRepo) ListByCardIDs(ctx context.Context, cardIDs []uuid.UUID) (map[uuid.UUID][]domain.CardSourceLinkWithChunk, error) {
	out := make(map[uuid.UUID][]domain.CardSourceLinkWithChunk, len(cardIDs))
	if len(cardIDs) == 0 {
		return out, nil
	}

	// Use ANY($1) with uuid[]
	rows, err := r.Pool.Query(ctx, `
		select l.card_id, c.id, c.ord, c.section_path, l.relevance
		from card_source_links l
		join chunks c on c.id = l.chunk_id
		where l.card_id = any($1)
		order by l.card_id, l.relevance desc nulls last, c.ord asc
	`, cardIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var cardID uuid.UUID
		var chunkID uuid.UUID
		var ord int
		var sectionPath *string
		var rel *float64
		if err := rows.Scan(&cardID, &chunkID, &ord, &sectionPath, &rel); err != nil {
			return nil, err
		}
		out[cardID] = append(out[cardID], domain.CardSourceLinkWithChunk{ChunkID: chunkID, Ord: ord, SectionPath: sectionPath, Relevance: rel})
	}
	return out, rows.Err()
}

func (r CardSourceLinksRepo) InsertBatch(ctx context.Context, tx pgxTx, links []domain.CardSourceLink) error {
	if len(links) == 0 {
		return nil
	}
	for _, l := range links {
		_, err := tx.Exec(ctx, `
			insert into card_source_links (card_id, chunk_id, relevance)
			values ($1,$2,$3)
			on conflict (card_id, chunk_id) do nothing
		`, l.CardID, l.ChunkID, l.Relevance)
		if err != nil {
			return err
		}
	}
	return nil
}

// pgxTx is a tiny interface to allow inserting links inside an existing transaction.
// It is defined here to avoid importing pgx into usecase layer.
type pgxTx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Compile-time checks.
var _ usecase.CardSourceLinkRepository = CardSourceLinksRepo{}

// NOTE: we intentionally keep InsertBatch as a helper used by CardsRepo.InsertBatchWithLinks,
// so that card + links are committed atomically.
