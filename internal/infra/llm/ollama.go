package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pgvector/pgvector-go"
	"net/http"
	"os"
	"time"
)

type OllamaEmbedder struct {
	http      *http.Client
	baseURL   string // например: http://ollama:11434 или http://localhost:11434
	model     string // nomic-embed-text:v1.5
	truncate  bool
	keepAlive string // например "5m"
	// dimensions *int // можно добавить позже, если захочешь управлять размерностью
}

func NewOllamaEmbedder() *OllamaEmbedder {
	base := os.Getenv("OLLAMA_BASE_URL")
	if base == "" {
		base = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_EMBED_MODEL")
	if model == "" {
		model = "nomic-embed-text:v1.5"
	}
	keepAlive := os.Getenv("OLLAMA_KEEP_ALIVE")
	if keepAlive == "" {
		keepAlive = "5m"
	}

	return &OllamaEmbedder{
		http:      &http.Client{Timeout: 30 * time.Second},
		baseURL:   base,
		model:     model,
		truncate:  true,
		keepAlive: keepAlive,
	}
}

type ollamaEmbedReq struct {
	Model     string `json:"model"`
	Input     string `json:"input"` // Ollama docs также поддерживают массив строк
	Truncate  bool   `json:"truncate,omitempty"`
	KeepAlive string `json:"keep_alive,omitempty"`
	// Dimensions *int `json:"dimensions,omitempty"`
}

type ollamaEmbedResp struct {
	Model         string      `json:"model"`
	Embeddings    [][]float64 `json:"embeddings"`
	TotalDuration int64       `json:"total_duration,omitempty"`
	LoadDuration  int64       `json:"load_duration,omitempty"`
	PromptEvalCnt int64       `json:"prompt_eval_count,omitempty"`
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) (pgvector.Vector, error) {
	reqBody := ollamaEmbedReq{
		Model:     e.model,
		Input:     text,
		Truncate:  e.truncate,
		KeepAlive: e.keepAlive,
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("marshal ollama embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embed", bytes.NewReader(b))
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("create ollama embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("call ollama /api/embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return pgvector.Vector{}, fmt.Errorf("ollama /api/embed status=%d", resp.StatusCode)
	}

	var out ollamaEmbedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return pgvector.Vector{}, fmt.Errorf("decode ollama embed response: %w", err)
	}
	if len(out.Embeddings) == 0 || len(out.Embeddings[0]) == 0 {
		return pgvector.Vector{}, fmt.Errorf("ollama returned empty embeddings")
	}

	vec := make([]float32, len(out.Embeddings[0]))
	for i, v := range out.Embeddings[0] {
		vec[i] = float32(v)
	}

	return pgvector.NewVector(vec), nil
}
