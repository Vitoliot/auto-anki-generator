package postgres

import (
	"context"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SourcesRepo struct{ Pool *pgxpool.Pool }

func (r SourcesRepo) Create(ctx context.Context, s domain.Source) error {
	_, err := r.Pool.Exec(ctx, `
		insert into sources (id, user_id, title, type, original_filename, language, status, page_count, storage_path, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, s.ID, s.UserID, s.Title, string(s.Type), s.OriginalFilename, s.Language, string(s.Status), s.PageCount, s.StoragePath, s.CreatedAt, s.UpdatedAt)
	return err
}

func (r SourcesRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]domain.Source, error) {
	rows, err := r.Pool.Query(ctx, `
		select id, user_id, title, type, original_filename, language, status, page_count, storage_path, created_at, updated_at
		from sources where user_id=$1 order by created_at desc
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Source
	for rows.Next() {
		var s domain.Source
		var typ, st string
		if err := rows.Scan(&s.ID, &s.UserID, &s.Title, &typ, &s.OriginalFilename, &s.Language, &st, &s.PageCount, &s.StoragePath, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Type = domain.SourceType(typ)
		s.Status = domain.SourceStatus(st)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r SourcesRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.Source, error) {
	row := r.Pool.QueryRow(ctx, `
		select id, user_id, title, type, original_filename, language, status, page_count, storage_path, created_at, updated_at
		from sources where id=$1
	`, id)
	var s domain.Source
	var typ, st string
	if err := row.Scan(&s.ID, &s.UserID, &s.Title, &typ, &s.OriginalFilename, &s.Language, &st, &s.PageCount, &s.StoragePath, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return domain.Source{}, err
	}
	s.Type = domain.SourceType(typ)
	s.Status = domain.SourceStatus(st)
	return s, nil
}

func (r SourcesRepo) UpdateStatus(ctx context.Context, id uuid.UUID, from, to domain.SourceStatus) (bool, error) {
	ct, err := r.Pool.Exec(ctx, `
		update sources set status=$2, updated_at=now()
		where id=$1 and status=$3
	`, id, string(to), string(from))
	return ct.RowsAffected() == 1, err
}

func (r SourcesRepo) Touch(ctx context.Context, id uuid.UUID, t time.Time) error {
	_, err := r.Pool.Exec(ctx, `update sources set updated_at=$2 where id=$1`, id, t)
	return err
}

var _ usecase.SourceRepository = SourcesRepo{}
