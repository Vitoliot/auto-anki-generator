package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/example/auto-anki/internal/domain"
	"github.com/google/uuid"
)

var ErrNotReady = errors.New("not ready")

type ExportService struct {
	Exports  ExportJobRepository
	Cards    CardRepository
	Links    CardSourceLinkRepository
	Exporter Exporter
	Clock    Clock
	Files    FileStore
}

type CreateExportInput struct {
	UserID    uuid.UUID
	CardSetID uuid.UUID
	Format    domain.ExportFormat
}

func (s ExportService) CreateAndRun(ctx context.Context, in CreateExportInput) (domain.ExportJob, error) {
	id := uuid.New()
	j := domain.ExportJob{
		ID:        id,
		UserID:    in.UserID,
		CardSetID: in.CardSetID,
		Format:    in.Format,
		Status:    domain.JobPending,
		CreatedAt: s.Clock.Now(),
	}
	if err := s.Exports.Create(ctx, j); err != nil {
		return domain.ExportJob{}, err
	}

	// Prototype: делаем синхронно в API, чтобы не плодить воркеры.
	if ok, err := s.Exports.MarkRunning(ctx, j.ID, s.Clock.Now()); err != nil {
		return domain.ExportJob{}, err
	} else if !ok {
		return domain.ExportJob{}, errors.New("export already running")
	}

	cards, err := s.Cards.List(ctx, CardFilter{
		UserID:    in.UserID,
		CardSetID: &in.CardSetID,
		Status:    ptr(domain.CardApproved),
		Limit:     5000,
		Offset:    0,
	})
	if err != nil {
		_, _ = s.Exports.MarkFinished(ctx, j.ID, domain.JobFailed, s.Clock.Now(), nil, ptr("failed to load cards"))
		return domain.ExportJob{}, err
	}

	// Enrich export with citations from card_source_links.
	if s.Links != nil && len(cards) > 0 {
		ids := make([]uuid.UUID, 0, len(cards))
		for _, c := range cards {
			ids = append(ids, c.ID)
		}
		m, err := s.Links.ListByCardIDs(ctx, ids)
		if err == nil {
			for i := range cards {
				links := m[cards[i].ID]
				if len(links) == 0 {
					continue
				}
				var b strings.Builder
				b.WriteString(cards[i].Answer)
				b.WriteString("\n\nИсточники:\n")
				for _, l := range links {
					sec := ""
					if l.SectionPath != nil {
						sec = *l.SectionPath
					} else {
						sec = fmt.Sprintf("chunk#%d", l.Ord)
					}
					b.WriteString("- ")
					b.WriteString(sec)
					b.WriteString("\n")
				}
				cards[i].Answer = b.String()
			}
		}
	}
	ext := "csv"
	if in.Format == domain.ExportAPKG {
		ext = "apkg"
	}
	outPath, err := s.Files.PrepareExport(ctx, j.ID, ext)
	if err != nil {
		return domain.ExportJob{}, err
	}

	switch in.Format {
	case domain.ExportCSV:
		err = s.Exporter.ExportCSV(ctx, cards, outPath)
	case domain.ExportAPKG:
		err = errors.New("APKG exporter not implemented in prototype")
	default:
		err = errors.New("unknown format")
	}

	if err != nil {
		msg := err.Error()
		_, _ = s.Exports.MarkFinished(ctx, j.ID, domain.JobFailed, s.Clock.Now(), nil, &msg)
		return domain.ExportJob{}, err
	}

	_, _ = s.Exports.MarkFinished(ctx, j.ID, domain.JobReady, s.Clock.Now(), &outPath, nil)
	j.FilePath = &outPath
	j.Status = domain.JobReady
	return j, nil
}

func (s ExportService) Get(ctx context.Context, userID, exportID uuid.UUID) (domain.ExportJob, error) {
	j, err := s.Exports.FindByID(ctx, exportID)
	if err != nil {
		return domain.ExportJob{}, err
	}
	if j.UserID != userID {
		return domain.ExportJob{}, ErrForbidden
	}
	return j, nil
}

func ptr[T any](v T) *T { return &v }
