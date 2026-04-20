package domain

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Name         string
	Role         Role
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

type SourceType string
type SourceStatus string

const (
	SourcePDF      SourceType = "pdf"
	SourceDOCX     SourceType = "docx"
	SourceHTML     SourceType = "html"
	SourceMarkdown SourceType = "markdown"
	SourceText     SourceType = "text"

	SourceUploaded SourceStatus = "uploaded"
	SourceParsed   SourceStatus = "parsed"
	SourceIndexed  SourceStatus = "indexed"
	SourceError    SourceStatus = "error"
)

type Source struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	Title            string
	Type             SourceType
	OriginalFilename string
	Language         *string
	Status           SourceStatus
	PageCount        *int
	StoragePath      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Chunk struct {
	ID          uuid.UUID
	SourceID    uuid.UUID
	Ord         int
	SectionPath *string
	Text        string
	TokenCount  *int
	CreatedAt   time.Time
}

type CardSetOrigin string

const (
	OriginManual    CardSetOrigin = "manual"
	OriginGenerated CardSetOrigin = "generated"
)

type CardSet struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Title       string
	Description *string
	Origin      CardSetOrigin
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type JobStatus string

const (
	JobPending JobStatus = "pending"
	JobRunning JobStatus = "running"
	JobReady   JobStatus = "ready"
	JobFailed  JobStatus = "failed"
)

type CardType string

const (
	CardTypeBasic CardType = "basic"
	CardTypeCloze CardType = "cloze"
	CardTypeMixed CardType = "mixed"
)

type GenerationJob struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	SourceID       uuid.UUID
	CardSetID      *uuid.UUID
	Status         JobStatus
	RequestedAt    time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CardType       CardType
	MaxCards       int
	TargetLang     *string
	ModelName      *string
	Temperature    *float64
	TopK           *int
	Seed           *int
	TotalGenerated int
	DuplicateCount int
	AvgAnswerLen   int
	CoverageScore  *float64
}

type CardStatus string

const (
	CardDraft    CardStatus = "draft"
	CardApproved CardStatus = "approved"
	CardRejected CardStatus = "rejected"
)

type Card struct {
	ID              uuid.UUID
	CardSetID       *uuid.UUID
	GenerationJobID *uuid.UUID
	UserID          uuid.UUID
	Type            string // "basic" | "cloze"
	Question        string
	Answer          string
	Extra           *string
	Status          CardStatus
	Difficulty      *int
	SRSInterval     *int
	SRSDueAt        *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CardSourceLink stores a traceable connection between a generated card and the chunk(s)
// that support it. This is used for citations and post-validation.
type CardSourceLink struct {
	CardID    uuid.UUID
	ChunkID   uuid.UUID
	Relevance *float64
}

// CardSourceLinkWithChunk is a denormalized view for rendering/export.
type CardSourceLinkWithChunk struct {
	ChunkID     uuid.UUID
	Ord         int
	SectionPath *string
	Relevance   *float64
}

type ExportFormat string

const (
	ExportCSV  ExportFormat = "csv"
	ExportAPKG ExportFormat = "apkg"
)

type ExportJob struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	CardSetID    uuid.UUID
	Format       ExportFormat
	Status       JobStatus
	FilePath     *string
	CreatedAt    time.Time
	FinishedAt   *time.Time
	ErrorMessage *string
}
