package usecase

import (
	"fmt"
	"strings"

	"github.com/example/auto-anki/internal/domain"
)

// BuildRAGPrompt constructs a "grounded" prompt: the model must only use provided chunks and
// must attach citations (chunk IDs) to each generated card.
//
// In a real integration this prompt is sent to an LLM provider.
func BuildRAGPrompt(targetLang string, cardType domain.CardType, chunks []ChunkWithScore, maxCards int) string {
	if targetLang == "" {
		targetLang = "ru"
	}
	if maxCards <= 0 {
		maxCards = 5
	}

	var b strings.Builder
	b.WriteString("Ты — помощник, который генерирует учебные карточки интервального повторения (SRS).\n")
	b.WriteString("ОГРАНИЧЕНИЕ: используй ТОЛЬКО факты из предоставленных источников.\n")
	b.WriteString("Для каждого утверждения в ответе должна быть хотя бы одна ссылка на источник.\n")
	b.WriteString("Выводи результат в JSON без пояснений.\n\n")
	b.WriteString("Формат ответа:\n")
	b.WriteString(`{"cards":[{"type":"basic|cloze","question":"...","answer":"...","citations":["<chunk_id>","..."],"extra":"optional"}]}`)
	b.WriteString("\n\n")
	b.WriteString("Требования к карточкам:\n")
	b.WriteString(fmt.Sprintf("- Язык: %s\n", targetLang))
	b.WriteString(fmt.Sprintf("- Тип: %s\n", string(cardType)))
	b.WriteString(fmt.Sprintf("- Количество: до %d\n", maxCards))
	b.WriteString("- Вопрос краткий и однозначный.\n")
	b.WriteString("- Ответ короткий, без воды, без новых фактов.\n\n")

	b.WriteString("ИСТОЧНИКИ (chunks):\n")
	for _, c := range chunks {
		sec := ""
		if c.SectionPath != nil {
			sec = *c.SectionPath
		} else {
			sec = fmt.Sprintf("chunk#%d", c.Ord)
		}
		b.WriteString(fmt.Sprintf("- id=%s section=%s score=%.3f\n", c.ID.String(), sec, c.Score))
		b.WriteString("  text: ")
		b.WriteString(truncateRunes(strings.TrimSpace(c.Text), 600))
		b.WriteString("\n")
	}

	return b.String()
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
