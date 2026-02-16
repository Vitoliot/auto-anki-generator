package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com"
	defaultOpenAIModel   = "gpt-5-mini"
	defaultHTTPTimeout   = 60 * time.Second

	maxErrorBodyBytes = 4096
	maxRawPreview     = 500
)

type OpenAIConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
}

type OpenAICardLLM struct {
	client *http.Client
	apiKey string
	base   string
	model  string
}

// Backward-compatible constructor.
func NewOpenAICardLLM() *OpenAICardLLM {
	c, err := NewOpenAICardLLMFromEnv()
	if err != nil {
		panic(err)
	}
	return c
}

func NewOpenAICardLLMFromEnv() (*OpenAICardLLM, error) {
	cfg := OpenAIConfig{
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
		Model:   os.Getenv("OPENAI_MODEL"),
	}
	return NewOpenAICardLLMWithConfig(cfg, nil)
}

func NewOpenAICardLLMWithConfig(cfg OpenAIConfig, httpClient *http.Client) (*OpenAICardLLM, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, PermanentError{Err: fmt.Errorf("OPENAI_API_KEY is empty")}
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultOpenAIBaseURL
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = defaultOpenAIModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHTTPTimeout
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, PermanentError{Err: fmt.Errorf("invalid OPENAI_BASE_URL: %w", err)}
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, PermanentError{Err: fmt.Errorf("invalid OPENAI_BASE_URL: must include scheme and host")}
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	} else if httpClient.Timeout <= 0 {
		httpClient.Timeout = cfg.Timeout
	}

	return &OpenAICardLLM{
		client: httpClient,
		apiKey: cfg.APIKey,
		base:   strings.TrimRight(cfg.BaseURL, "/"),
		model:  cfg.Model,
	}, nil
}

// ---- Responses API DTOs ----

type responsesReq struct {
	Model           string              `json:"model"`
	Input           string              `json:"input"`
	Instructions    string              `json:"instructions,omitempty"`
	Temperature     float64             `json:"temperature,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Text            *responsesTextParam `json:"text,omitempty"`
	Truncation      string              `json:"truncation,omitempty"`
}

type responsesTextParam struct {
	Format responsesTextFormat `json:"format"`
}

type responsesTextFormat struct {
	Type        string         `json:"type"` // "json_schema" | "json_object"
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
}

type responsesResp struct {
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

type cardsJSON struct {
	Cards []struct {
		Type      string   `json:"type"`
		Question  string   `json:"question"`
		Answer    string   `json:"answer"`
		Citations []string `json:"citations"`
		Extra     *string  `json:"extra"`
	} `json:"cards"`
}

// ---- Typed errors for queue retry/DLQ classification ----

type TemporaryError struct{ Err error }
type PermanentError struct{ Err error }

func (e TemporaryError) Error() string {
	if e.Err == nil {
		return "temporary error"
	}
	return e.Err.Error()
}
func (e TemporaryError) Unwrap() error { return e.Err }

func (e PermanentError) Error() string {
	if e.Err == nil {
		return "permanent error"
	}
	return e.Err.Error()
}
func (e PermanentError) Unwrap() error { return e.Err }

func IsTemporary(err error) bool {
	var te TemporaryError
	return errors.As(err, &te)
}

func IsPermanent(err error) bool {
	var pe PermanentError
	return errors.As(err, &pe)
}

// ---- Public API ----

func (c *OpenAICardLLM) GenerateCards(ctx context.Context, req usecase.GenerateCardsRequest) ([]usecase.GeneratedCard, error) {
	if err := validateGenerateRequest(req); err != nil {
		return nil, PermanentError{Err: err}
	}

	out, err := c.callResponsesAPI(ctx, req.Prompt)
	if err != nil {
		return nil, err
	}

	text, err := extractAssistantOutputText(out)
	if err != nil {
		return nil, PermanentError{Err: fmt.Errorf("extract assistant output text: %w", err)}
	}

	cj, err := decodeCardsJSON(text)
	if err != nil {
		return nil, PermanentError{Err: err}
	}

	if err := validateCardsJSON(cj); err != nil {
		return nil, PermanentError{Err: err}
	}

	res, err := mapCardsJSONToDomain(cj)
	if err != nil {
		return nil, PermanentError{Err: err}
	}
	return res, nil
}

// ---- Request building / call ----

func validateGenerateRequest(req usecase.GenerateCardsRequest) error {
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("generate cards request has empty prompt")
	}
	return nil
}

func (c *OpenAICardLLM) callResponsesAPI(ctx context.Context, prompt string) (responsesResp, error) {
	payload, err := json.Marshal(responsesReq{
		Model:        c.model,
		Input:        prompt,
		Instructions: cardJSONInstructions(),
		// Ниже температура, чтобы снизить "творческий" шум и повысить структурную стабильность.
		Temperature:     0.1,
		MaxOutputTokens: 4000,
		// Важно: если вход слишком большой, лучше пусть API вернёт ошибку, чем тихо обрежет контекст.
		Truncation: "disabled",
		Text: &responsesTextParam{
			Format: responsesTextFormat{
				Type:        "json_schema",
				Name:        "anki_cards_response",
				Description: "Structured JSON for generated flashcards with citations to chunk UUIDs",
				Strict:      true,
				Schema:      cardsResponseSchema(),
			},
		},
	})
	if err != nil {
		return responsesResp{}, PermanentError{Err: fmt.Errorf("marshal openai responses request: %w", err)}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/responses", bytes.NewReader(payload))
	if err != nil {
		return responsesResp{}, PermanentError{Err: fmt.Errorf("build openai request: %w", err)}
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return responsesResp{}, fmt.Errorf("call openai responses: %w", err)
		}
		return responsesResp{}, TemporaryError{Err: fmt.Errorf("call openai responses: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		bodyPreview, _ := readBodyPreview(resp.Body, maxErrorBodyBytes)
		reqID := resp.Header.Get("x-request-id")
		e := fmt.Errorf("openai responses failed: status=%d request_id=%s body=%q", resp.StatusCode, reqID, bodyPreview)
		if isTemporaryHTTPStatus(resp.StatusCode) {
			return responsesResp{}, TemporaryError{Err: e}
		}
		return responsesResp{}, PermanentError{Err: e}
	}

	var out responsesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return responsesResp{}, PermanentError{Err: fmt.Errorf("decode openai responses: %w", err)}
	}

	return out, nil
}

// ---- Structured output helpers ----

// Важно: даже со Structured Outputs полезно явно сказать, что нужен только JSON без markdown.
// Это снижает число "краевых" случаев (например, ```json ... ``` в text output).
func cardJSONInstructions() string {
	return strings.Join([]string{
		"Return ONLY valid JSON that matches the provided schema.",
		"Do not add markdown fences, comments, or extra text.",
		"If information is insufficient, return an empty cards array only if schema allows it; otherwise produce the best valid cards grounded in the prompt.",
		"For citations, output only UUID strings.",
	}, " ")
}

func cardsResponseSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cards": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": map[string]any{
							"type": "string",
							// при желании:
							// "enum": []string{"basic", "cloze"},
						},
						"question": map[string]any{"type": "string"},
						"answer":   map[string]any{"type": "string"},
						"citations": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
						"extra": map[string]any{
							"type": []any{"string", "null"},
						},
					},
					"required": []string{
						"type",
						"question",
						"answer",
						"citations",
						"extra", // <-- обязательно добавить
					},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"cards"},
		"additionalProperties": false,
	}
}

// ---- Response parsing ----

func extractAssistantOutputText(out responsesResp) (string, error) {
	var sb strings.Builder

	for _, item := range out.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				sb.WriteString(part.Text)
			}
		}
	}

	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("empty output_text")
	}
	return text, nil
}

func decodeCardsJSON(text string) (cardsJSON, error) {
	var cj cardsJSON
	if err := json.Unmarshal([]byte(text), &cj); err != nil {
		return cardsJSON{}, fmt.Errorf("llm returned non-json or invalid schema: %w; raw=%q", err, truncate(text, maxRawPreview))
	}
	return cj, nil
}

func validateCardsJSON(cj cardsJSON) error {
	// Оставляю нестрогий вариант: пустой массив допустим (полезно на плохом входе).
	// Если хочешь, можно вернуть ошибку при len==0 и отправлять в retry/DLQ.
	if cj.Cards == nil {
		return fmt.Errorf("llm returned null/absent cards")
	}

	for i, card := range cj.Cards {
		if strings.TrimSpace(card.Type) == "" {
			return fmt.Errorf("card[%d]: empty type", i)
		}
		if strings.TrimSpace(card.Question) == "" {
			return fmt.Errorf("card[%d]: empty question", i)
		}
		if strings.TrimSpace(card.Answer) == "" {
			return fmt.Errorf("card[%d]: empty answer", i)
		}
		for j, c := range card.Citations {
			if strings.TrimSpace(c) == "" {
				return fmt.Errorf("card[%d]: empty citation[%d]", i, j)
			}
			if _, err := uuid.Parse(c); err != nil {
				return fmt.Errorf("card[%d]: invalid citation[%d]=%q: %w", i, j, c, err)
			}
		}
	}
	return nil
}

func mapCardsJSONToDomain(cj cardsJSON) ([]usecase.GeneratedCard, error) {
	res := make([]usecase.GeneratedCard, 0, len(cj.Cards))

	for i, card := range cj.Cards {
		ids := make([]uuid.UUID, 0, len(card.Citations))
		for j, s := range card.Citations {
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("card[%d]: parse citation[%d]=%q: %w", i, j, s, err)
			}
			ids = append(ids, id)
		}

		res = append(res, usecase.GeneratedCard{
			Type:             strings.TrimSpace(card.Type),
			Question:         strings.TrimSpace(card.Question),
			Answer:           strings.TrimSpace(card.Answer),
			Extra:            trimStringPtr(card.Extra),
			CitationChunkIDs: ids,
		})
	}

	return res, nil
}

// ---- misc helpers ----

func trimStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

func isTemporaryHTTPStatus(code int) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500 && code <= 599
}

func readBodyPreview(r io.Reader, n int64) (string, error) {
	if r == nil || n <= 0 {
		return "", nil
	}
	b, err := io.ReadAll(io.LimitReader(r, n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
