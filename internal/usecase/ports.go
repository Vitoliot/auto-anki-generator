package usecase

import (
	"context"
	"io"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

type Clock interface {
	Now() time.Time
}

// FileStore abstracts file I/O (local FS, S3, etc.).
// The returned string is an opaque storage key (for local FS it's a path).
//
// Keeping this as a port makes use cases testable and removes direct os.* dependencies.
type FileStore interface {
	SaveSource(ctx context.Context, sourceID uuid.UUID, r io.Reader) (storageKey string, err error)
	OpenSource(ctx context.Context, storageKey string) (io.ReadCloser, error)
	PrepareExport(ctx context.Context, exportID uuid.UUID, ext string) (filePath string, err error)
}

type UserRepository interface {
	Create(ctx context.Context, u domain.User) error
	FindByEmail(ctx context.Context, email string) (domain.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	UpdateLastLogin(ctx context.Context, id uuid.UUID, t time.Time) error
}

type SourceRepository interface {
	Create(ctx context.Context, s domain.Source) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.Source, error)
	FindByID(ctx context.Context, id uuid.UUID) (domain.Source, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, from, to domain.SourceStatus) (bool, error)
	Touch(ctx context.Context, id uuid.UUID, t time.Time) error
}

type ChunkRepository interface {
	InsertBatch(ctx context.Context, sourceID uuid.UUID, chunks []ChunkToInsert) error
	SearchSimilar(ctx context.Context, sourceID uuid.UUID, query pgvector.Vector, topK int) ([]ChunkWithScore, error)
}

type ChunkToInsert struct {
	ID          uuid.UUID
	Ord         int
	SectionPath *string
	Text        string
	TokenCount  *int
	Embedding   pgvector.Vector
}

type ChunkWithScore struct {
	ID          uuid.UUID
	Ord         int
	SectionPath *string
	Text        string
	Score       float64 // cosine similarity-ish
}

type CardSetRepository interface {
	Create(ctx context.Context, cs domain.CardSet) error
	FindByID(ctx context.Context, id uuid.UUID) (domain.CardSet, error)
}

type GenerationJobRepository interface {
	Create(ctx context.Context, j domain.GenerationJob) error
	FindByID(ctx context.Context, id uuid.UUID) (domain.GenerationJob, error)
	MarkRunning(ctx context.Context, id uuid.UUID, startedAt time.Time) (bool, error)
	MarkFinished(ctx context.Context, id uuid.UUID, status domain.JobStatus, finishedAt time.Time, totalGenerated, dupCount, avgAnswerLen int, coverageScore *float64) (bool, error)
}

type CardRepository interface {
	InsertBatch(ctx context.Context, cards []domain.Card) error
	InsertBatchWithLinks(ctx context.Context, cards []domain.Card, links []domain.CardSourceLink) error
	List(ctx context.Context, filter CardFilter) ([]domain.Card, error)
	Update(ctx context.Context, id, userID uuid.UUID, patch CardPatch) (bool, error)
	CountApprovedByJob(ctx context.Context, jobID uuid.UUID) (int, error)
}

type CardSourceLinkRepository interface {
	ListByCardIDs(ctx context.Context, cardIDs []uuid.UUID) (map[uuid.UUID][]domain.CardSourceLinkWithChunk, error)
}

type CardFilter struct {
	UserID    uuid.UUID
	JobID     *uuid.UUID
	CardSetID *uuid.UUID
	Status    *domain.CardStatus
	Limit     int
	Offset    int
}

type CardPatch struct {
	Status   *domain.CardStatus
	Question *string
	Answer   *string
	Extra    *string
}

type ExportJobRepository interface {
	Create(ctx context.Context, j domain.ExportJob) error
	FindByID(ctx context.Context, id uuid.UUID) (domain.ExportJob, error)
	MarkRunning(ctx context.Context, id uuid.UUID, startedAt time.Time) (bool, error)
	MarkFinished(ctx context.Context, id uuid.UUID, status domain.JobStatus, finishedAt time.Time, filePath *string, errMsg *string) (bool, error)
}

type Queue interface {
	EnqueueParseSource(ctx context.Context, sourceID uuid.UUID) error
	EnqueueGenerateCards(ctx context.Context, jobID uuid.UUID) error
}

type Embedder interface {
	Embed(ctx context.Context, text string) (pgvector.Vector, error)
}

type CardLLM interface {
	GenerateCards(ctx context.Context, req GenerateCardsRequest) ([]GeneratedCard, error)
}

type GenerateCardsRequest struct {
	TargetLanguage string
	CardType       domain.CardType
	ContextChunks  []ChunkWithScore
	MaxCards       int
	Prompt         string
}

type GeneratedCard struct {
	Type             string
	Question         string
	Answer           string
	Extra            *string
	CitationChunkIDs []uuid.UUID
}

type Exporter interface {
	ExportCSV(ctx context.Context, cards []domain.Card, filePath string) error
	// ExportAPKG(ctx context.Context, cards []domain.Card, filePath string) error // TODO
}
