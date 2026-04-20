package postgres

import (
	"context"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ExportJobsRepo struct{ Pool *pgxpool.Pool }

func (r ExportJobsRepo) Create(ctx context.Context, j domain.ExportJob) error {
	_, err := r.Pool.Exec(ctx, `
		insert into export_jobs (id, user_id, card_set_id, format, status, file_path, created_at)
		values ($1,$2,$3,$4,$5,$6,$7)
	`, j.ID, j.UserID, j.CardSetID, string(j.Format), string(j.Status), j.FilePath, j.CreatedAt)
	return err
}

func (r ExportJobsRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.ExportJob, error) {
	row := r.Pool.QueryRow(ctx, `
		select id, user_id, card_set_id, format, status, file_path, created_at, finished_at, error_message
		from export_jobs where id=$1
	`, id)
	var j domain.ExportJob
	var format, status string
	if err := row.Scan(&j.ID, &j.UserID, &j.CardSetID, &format, &status, &j.FilePath, &j.CreatedAt, &j.FinishedAt, &j.ErrorMessage); err != nil {
		return domain.ExportJob{}, err
	}
	j.Format = domain.ExportFormat(format)
	j.Status = domain.JobStatus(status)
	return j, nil
}

func (r ExportJobsRepo) MarkRunning(ctx context.Context, id uuid.UUID, startedAt time.Time) (bool, error) {
	ct, err := r.Pool.Exec(ctx, `
		update export_jobs set status='running'
		where id=$1 and status='pending'
	`, id)
	_ = startedAt // хранение startedAt можно добавить отдельной колонкой; в ERD её нет.
	return ct.RowsAffected() == 1, err
}

func (r ExportJobsRepo) MarkFinished(ctx context.Context, id uuid.UUID, status domain.JobStatus, finishedAt time.Time, filePath *string, errMsg *string) (bool, error) {
	ct, err := r.Pool.Exec(ctx, `
		update export_jobs
		set status=$2, finished_at=$3, file_path=$4, error_message=$5
		where id=$1 and status='running'
	`, id, string(status), finishedAt, filePath, errMsg)
	return ct.RowsAffected() == 1, err
}

var _ usecase.ExportJobRepository = ExportJobsRepo{}
