package usecase

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

type CardValidation struct {
	Score  float64  `json:"score"`
	OK     bool     `json:"ok"`
	Issues []string `json:"issues,omitempty"`
}

// ValidateGrounding is a lightweight post-check that the answer is "supported" by cited chunks.
// For a production system you'd typically add LLM-based self-checking, entailment models, or
// retrieval-based fact validators. Here we use lexical overlap as a pragmatic prototype signal.
func ValidateGrounding(answer string, cited []ChunkWithScore) CardValidation {
	ansToks := tokenSet(answer)
	if len(ansToks) == 0 {
		return CardValidation{Score: 0, OK: false, Issues: []string{"empty_answer"}}
	}
	if len(cited) == 0 {
		return CardValidation{Score: 0, OK: false, Issues: []string{"no_citations"}}
	}

	// Compute max overlap ratio across cited chunks.
	best := 0.0
	for _, ch := range cited {
		o := overlapRatio(ansToks, tokenSet(ch.Text))
		if o > best {
			best = o
		}
	}

	ok := best >= 0.12 // heuristic threshold for short answers
	issues := []string{}
	if !ok {
		issues = append(issues, "low_overlap")
	}
	return CardValidation{Score: best, OK: ok, Issues: issues}
}

// BuildValidationExtra returns a compact JSON string to store in cards.extra.
func BuildValidationExtra(v CardValidation, citationIDs []string) *string {
	obj := map[string]any{
		"validation": v,
		"citations":  citationIDs,
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

func tokenSet(s string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, t := range tokenRe.FindAllString(strings.ToLower(s), -1) {
		if len(t) <= 2 {
			continue
		}
		m[t] = struct{}{}
	}
	return m
}

func overlapRatio(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	// Normalize by min size to avoid penalizing short answers too hard.
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	return float64(inter) / float64(min)
}

// PickCitedChunks maps citation IDs to their chunk objects.
func PickCitedChunks(ctxChunks []ChunkWithScore, citationIDs []string) []ChunkWithScore {
	idx := make(map[string]ChunkWithScore, len(ctxChunks))
	for _, c := range ctxChunks {
		idx[c.ID.String()] = c
	}
	var out []ChunkWithScore
	for _, id := range citationIDs {
		if c, ok := idx[id]; ok {
			out = append(out, c)
		}
	}
	// Stable order by score desc (better citations first).
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
