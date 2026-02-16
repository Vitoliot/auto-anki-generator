package postgres

import (
	"context"
	"math"

	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type ChunksRepo struct{ Pool *pgxpool.Pool }

func (r ChunksRepo) InsertBatch(ctx context.Context, sourceID uuid.UUID, chunks []usecase.ChunkToInsert) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Simple loop in tx for prototype.
	for _, c := range chunks {
		_, err := tx.Exec(ctx, `
			insert into chunks (id, source_id, ord, section_path, text, token_count, embedding, created_at)
			values ($1,$2,$3,$4,$5,$6,$7, now())
			on conflict (source_id, ord) do nothing
		`, c.ID, sourceID, c.Ord, c.SectionPath, c.Text, c.TokenCount, c.Embedding)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (r ChunksRepo) SearchSimilar(ctx context.Context, sourceID uuid.UUID, query pgvector.Vector, topK int) ([]usecase.ChunkWithScore, error) {
	if topK <= 0 {
		topK = 5
	}
	rows, err := r.Pool.Query(ctx, `
		select id, ord, section_path, text, 1 - (embedding <=> $2) as score
		from chunks
		where source_id=$1
		order by embedding <=> $2
		limit $3
	`, sourceID, query, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []usecase.ChunkWithScore
	for rows.Next() {
		var c usecase.ChunkWithScore
		if err := rows.Scan(&c.ID, &c.Ord, &c.SectionPath, &c.Text, &c.Score); err != nil {
			return nil, err
		}
		// protect from NaN if embedding null
		if math.IsNaN(c.Score) {
			c.Score = 0
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var _ usecase.ChunkRepository = ChunksRepo{}
