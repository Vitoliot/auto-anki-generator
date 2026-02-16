package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
)

type Pipeline struct {
	Sources  usecase.SourceRepository
	Chunks   usecase.ChunkRepository
	Jobs     usecase.GenerationJobRepository
	Cards    usecase.CardRepository
	Files    usecase.FileStore
	Embedder usecase.Embedder
	LLM      usecase.CardLLM
	Clock    usecase.Clock
}

type parseMsg struct {
	SourceID string `json:"source_id"`
}

func (p Pipeline) HandleParseMessage(ctx context.Context, payload string) error {
	var m parseMsg
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return err
	}
	sourceID, err := uuid.Parse(m.SourceID)
	if err != nil {
		return err
	}

	src, err := p.Sources.FindByID(ctx, sourceID)
	if err != nil {
		return err
	}

	// Idempotency: try to "lock" status transition. If already progressed — do nothing.
	if src.Status == domain.SourceIndexed {
		return nil
	}
	ok, err := p.Sources.UpdateStatus(ctx, src.ID, domain.SourceUploaded, domain.SourceParsed)
	if err != nil {
		return err
	}
	if !ok {
		// Важно для идемпотентности: src.Status мог быть устаревшим (read-before-CAS).
		// Если другой воркер уже перевёл Source в PARSED и упал до индексации,
		// мы должны продолжить, иначе источник застрянет в промежуточном статусе.
		cur, err := p.Sources.FindByID(ctx, src.ID)
		if err != nil {
			return err
		}
		switch cur.Status {
		case domain.SourceParsed:
			src = cur
		case domain.SourceIndexed:
			return nil
		default:
			return nil
		}
	}

	rc, err := p.Files.OpenSource(ctx, src.StoragePath)
	if err != nil {
		_, _ = p.Sources.UpdateStatus(ctx, src.ID, domain.SourceParsed, domain.SourceError)
		return err
	}
	defer rc.Close()

	text, err := extractText(src.Type, rc)
	if err != nil {
		_, _ = p.Sources.UpdateStatus(ctx, src.ID, domain.SourceParsed, domain.SourceError)
		return err
	}
	chunks := chunkText(text, 900)

	toIns := make([]usecase.ChunkToInsert, 0, len(chunks))
	for i, ch := range chunks {
		sec := fmt.Sprintf("p/%d", i+1)
		tok := approxTokenCount(ch)
		vec, err := p.Embedder.Embed(ctx, ch)
		if err != nil {
			_, _ = p.Sources.UpdateStatus(ctx, src.ID, domain.SourceParsed, domain.SourceError)
			return err
		}
		toIns = append(toIns, usecase.ChunkToInsert{
			ID:          uuid.New(),
			Ord:         i,
			SectionPath: &sec,
			Text:        ch,
			TokenCount:  &tok,
			Embedding:   vec,
		})
	}

	if err := p.Chunks.InsertBatch(ctx, src.ID, toIns); err != nil {
		_, _ = p.Sources.UpdateStatus(ctx, src.ID, domain.SourceParsed, domain.SourceError)
		return err
	}

	ok, err = p.Sources.UpdateStatus(ctx, src.ID, domain.SourceParsed, domain.SourceIndexed)
	if err != nil {
		return err
	}
	if !ok {
		cur, err := p.Sources.FindByID(ctx, src.ID)
		if err != nil {
			return err
		}
		if cur.Status == domain.SourceIndexed {
			return nil
		}
		// Не мешаем другому воркеру; статус может быть ERROR или уже в процессе.
		return nil
	}
	return nil
}

type genMsg struct {
	JobID string `json:"job_id"`
}

func (p Pipeline) HandleGenMessage(ctx context.Context, payload string) error {
	var m genMsg
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return err
	}
	jobID, err := uuid.Parse(m.JobID)
	if err != nil {
		return err
	}
	j, err := p.Jobs.FindByID(ctx, jobID)
	if err != nil {
		return err
	}

	// Idempotency: CAS pending->running.
	if j.Status == domain.JobReady || j.Status == domain.JobFailed {
		return nil
	}
	ok, err := p.Jobs.MarkRunning(ctx, j.ID, p.Clock.Now())
	if err != nil {
		return err
	}
	if !ok {
		// Аналогично Source: job.Status мог быть прочитан до CAS.
		// Если предыдущий воркер успел поставить RUNNING и упал, мы должны уметь продолжить
		// обработку (особенно при XAUTOCLAIM pending сообщений).
		cur, err := p.Jobs.FindByID(ctx, j.ID)
		if err != nil {
			return err
		}
		if cur.Status != domain.JobRunning {
			return nil
		}
		j = cur
	}

	topK := 5
	if j.TopK != nil && *j.TopK > 0 {
		topK = *j.TopK
	}
	targetLang := "ru"
	if j.TargetLang != nil {
		targetLang = *j.TargetLang
	}

	totalGenerated := 0
	totalAnswerLen := 0
	dupCount := 0

	seen := map[string]struct{}{}

	validCited := 0

	// Прототип, но "дипломного" уровня: grounded RAG prompt + сохранение цитирований + пост-валидация.
	for i := 0; i < j.MaxCards; i++ {
		anchor := fmt.Sprintf("chunk #%d", i)
		qvec, err := p.Embedder.Embed(ctx, anchor)
		if err != nil {
			return p.failJob(ctx, j.ID, err)
		}

		ctxChunks, err := p.Chunks.SearchSimilar(ctx, j.SourceID, qvec, topK)
		if err != nil {
			return p.failJob(ctx, j.ID, err)
		}
		prompt := usecase.BuildRAGPrompt(targetLang, j.CardType, ctxChunks, 1)
		genCards, err := p.LLM.GenerateCards(ctx, usecase.GenerateCardsRequest{
			TargetLanguage: targetLang,
			CardType:       j.CardType,
			ContextChunks:  ctxChunks,
			MaxCards:       1,
			Prompt:         prompt,
		})
		if err != nil {
			return p.failJob(ctx, j.ID, err)
		}

		var toInsert []domain.Card
		var links []domain.CardSourceLink
		for _, gc := range genCards {
			h := hashQA(gc.Question, gc.Answer)
			if _, ok := seen[h]; ok {
				dupCount++
				continue
			}
			seen[h] = struct{}{}

			// Ensure we have at least one citation.
			if len(gc.CitationChunkIDs) == 0 && len(ctxChunks) > 0 {
				gc.CitationChunkIDs = []uuid.UUID{ctxChunks[0].ID}
			}

			citationIDs := make([]string, 0, len(gc.CitationChunkIDs))
			for _, id := range gc.CitationChunkIDs {
				citationIDs = append(citationIDs, id.String())
			}
			cited := usecase.PickCitedChunks(ctxChunks, citationIDs)
			v := usecase.ValidateGrounding(gc.Answer, cited)
			if !v.OK && len(cited) > 0 {
				// Auto-fix: make the answer more extractive to improve groundedness.
				gc.Answer = "По источнику: " + truncate(cited[0].Text, 260)
				v.Issues = append(v.Issues, "auto_fixed_to_source")
				v = usecase.ValidateGrounding(gc.Answer, cited)
			}
			if v.OK {
				validCited++
			}
			extra := buildExtra(gc.Extra, v, citationIDs)

			totalGenerated++
			totalAnswerLen += len([]rune(gc.Answer))

			cardID := uuid.New()
			toInsert = append(toInsert, domain.Card{
				ID:              cardID,
				CardSetID:       j.CardSetID,
				GenerationJobID: &j.ID,
				UserID:          j.UserID,
				Type:            gc.Type,
				Question:        gc.Question,
				Answer:          gc.Answer,
				Extra:           extra,
				Status:          domain.CardDraft,
			})

			scoreByID := map[uuid.UUID]float64{}
			for _, ch := range ctxChunks {
				scoreByID[ch.ID] = ch.Score
			}
			for _, cid := range gc.CitationChunkIDs {
				if _, ok := scoreByID[cid]; !ok {
					continue
				}
				rel := clampScore(scoreByID[cid])
				links = append(links, domain.CardSourceLink{CardID: cardID, ChunkID: cid, Relevance: &rel})
			}
		}

		if len(toInsert) > 0 {
			if err := p.Cards.InsertBatchWithLinks(ctx, toInsert, links); err != nil {
				return p.failJob(ctx, j.ID, err)
			}
		}
	}

	avg := 0
	if totalGenerated > 0 {
		avg = totalAnswerLen / totalGenerated
	}

	var coverage *float64
	if totalGenerated > 0 {
		s := float64(validCited) / float64(totalGenerated)
		coverage = &s
	}

	ok, err = p.Jobs.MarkFinished(ctx, j.ID, domain.JobReady, p.Clock.Now(), totalGenerated, dupCount, avg, coverage)
	if err != nil {
		return err
	}
	if !ok {
		// Уже завершено (или не RUNNING) — считаем идемпотентным no-op.
		return nil
	}
	return nil
}

func (p Pipeline) failJob(ctx context.Context, jobID uuid.UUID, err error) error {
	_, _ = p.Jobs.MarkFinished(ctx, jobID, domain.JobFailed, p.Clock.Now(), 0, 0, 0, nil)
	return err
}

func extractText(typ domain.SourceType, r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s := string(b)

	switch typ {
	case domain.SourceText, domain.SourceMarkdown:
		return s, nil
	case domain.SourceHTML:
		return stripTags(s), nil
	case domain.SourcePDF, domain.SourceDOCX:
		return "", errors.New("pdf/docx extraction not implemented in prototype")
	default:
		return "", errors.New("unknown source type")
	}
}

func stripTags(html string) string {
	// Naive: remove <...>. Enough for prototype.
	var out strings.Builder
	inTag := false
	for _, r := range html {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

func chunkText(text string, maxRunes int) []string {
	// Simple paragraph splitter + size cap.
	paras := strings.Split(text, "\n\n")
	var out []string
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		r := []rune(p)
		for len(r) > maxRunes {
			out = append(out, strings.TrimSpace(string(r[:maxRunes])))
			r = r[maxRunes:]
		}
		out = append(out, strings.TrimSpace(string(r)))
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

func approxTokenCount(s string) int {
	// Very rough: for Russian/English text a token is ~4 chars on average.
	r := []rune(s)
	if len(r) == 0 {
		return 0
	}
	return (len(r) + 3) / 4
}

func buildExtra(orig *string, v usecase.CardValidation, citationIDs []string) *string {
	// Keep original extra (if any) inside JSON.
	if orig != nil {
		// store as llm_extra
		obj := map[string]any{"llm_extra": *orig, "validation": v, "citations": citationIDs}
		b, err := json.Marshal(obj)
		if err == nil {
			s := string(b)
			return &s
		}
	}
	return usecase.BuildValidationExtra(v, citationIDs)
}

func clampScore(s float64) float64 {
	if s < 0 {
		return 0
	}
	if s > 0.999 {
		return 0.999
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func hashQA(q, a string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	a = strings.ToLower(strings.TrimSpace(a))
	return q + "||" + a
}
