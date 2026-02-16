package llm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"strings"

	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

type FakeEmbedder struct{}

func (FakeEmbedder) Embed(ctx context.Context, text string) (pgvector.Vector, error) {
	_ = ctx
	// Детерминированный "псевдо-вектор" (НЕ для продакшена).
	h := sha256.Sum256([]byte(text))
	vec := make([]float32, 1536)
	for i := 0; i < len(vec); i++ {
		// берём 4 байта из хеша по кругу
		j := (i * 4) % len(h)
		u := binary.LittleEndian.Uint32(h[j : j+4])
		// [0,1)
		vec[i] = float32(u) / float32(^uint32(0))
	}
	return pgvector.NewVector(vec), nil
}

type FakeCardLLM struct{}

func (FakeCardLLM) GenerateCards(ctx context.Context, req usecase.GenerateCardsRequest) ([]usecase.GeneratedCard, error) {
	_ = ctx
	// Прототип "grounded" генерации:
	// 1) Берём лучший по score chunk.
	// 2) Делаем короткий Q/A.
	// 3) Возвращаем citation на chunk.ID.
	var best *usecase.ChunkWithScore
	for i := range req.ContextChunks {
		c := &req.ContextChunks[i]
		if best == nil || c.Score > best.Score {
			best = c
		}
	}
	text := "Материал пуст."
	id := ""
	if best != nil {
		text = strings.TrimSpace(best.Text)
		id = best.ID.String()
	}
	first := text
	if i := strings.Index(first, "\n"); i >= 0 {
		first = first[:i]
	}
	q := "Что означает: " + truncate(first, 120) + "?"
	a := "По источнику: " + truncate(text, 240)

	out := []usecase.GeneratedCard{{
		Type:             "basic",
		Question:         q,
		Answer:           a,
		CitationChunkIDs: parseUUIDs(id),
	}}
	return out, nil
}

func parseUUIDs(s string) []uuid.UUID {
	if s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return []uuid.UUID{id}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
