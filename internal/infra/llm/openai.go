package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/pgvector/pgvector-go"
)

type OpenAIEmbedder struct {
	http   *http.Client
	apiKey string
	base   string
	model  string
}

func NewOpenAIEmbedder() *OpenAIEmbedder {
	key := os.Getenv("OPENAI_API_KEY")
	base := os.Getenv("OPENAI_BASE_URL")
	if base == "" {
		base = "https://api.openai.com"
	}
	model := os.Getenv("OPENAI_EMBED_MODEL")
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &OpenAIEmbedder{
		http:   &http.Client{Timeout: 20 * time.Second},
		apiKey: key,
		base:   base,
		model:  model,
	}
}

type embedReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResp struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) (pgvector.Vector, error) {
	body, _ := json.Marshal(embedReq{Model: e.model, Input: text})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.base+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return pgvector.Vector{}, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return pgvector.Vector{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return pgvector.Vector{}, fmt.Errorf("openai embeddings failed: status=%d", resp.StatusCode)
	}

	var out embedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return pgvector.Vector{}, err
	}
	if len(out.Data) == 0 {
		return pgvector.Vector{}, fmt.Errorf("openai embeddings: empty data")
	}

	vec := make([]float32, len(out.Data[0].Embedding))
	for i, v := range out.Data[0].Embedding {
		vec[i] = float32(v)
	}
	return pgvector.NewVector(vec), nil
}
