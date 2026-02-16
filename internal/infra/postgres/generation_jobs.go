package postgres

import (
	"context"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GenerationJobsRepo struct{ Pool *pgxpool.Pool }

func (r GenerationJobsRepo) Create(ctx context.Context, j domain.GenerationJob) error {
	_, err := r.Pool.Exec(ctx, `
		insert into generation_jobs (
		  id, user_id, source_id, card_set_id, status, requested_at,
		  card_type, max_cards, target_language, model_name, temperature, top_k, seed,
		  total_cards_generated, duplicate_count, avg_answer_length, coverage_score
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0,0,0,null)
	`, j.ID, j.UserID, j.SourceID, j.CardSetID, string(j.Status), j.RequestedAt,
		string(j.CardType), j.MaxCards, j.TargetLang, j.ModelName, j.Temperature, j.TopK, j.Seed,
	)
	return err
}

func (r GenerationJobsRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.GenerationJob, error) {
	row := r.Pool.QueryRow(ctx, `
		select id, user_id, source_id, card_set_id, status, requested_at, started_at, finished_at,
		       card_type, max_cards, target_language, model_name, temperature, top_k, seed,
		       total_cards_generated, duplicate_count, avg_answer_length, coverage_score
		from generation_jobs where id=$1
	`, id)

	var j domain.GenerationJob
	var st, ct string
	if err := row.Scan(
		&j.ID, &j.UserID, &j.SourceID, &j.CardSetID, &st, &j.RequestedAt, &j.StartedAt, &j.FinishedAt,
		&ct, &j.MaxCards, &j.TargetLang, &j.ModelName, &j.Temperature, &j.TopK, &j.Seed,
		&j.TotalGenerated, &j.DuplicateCount, &j.AvgAnswerLen, &j.CoverageScore,
	); err != nil {
		return domain.GenerationJob{}, err
	}
	j.Status = domain.JobStatus(st)
	j.CardType = domain.CardType(ct)
	return j, nil
}

func (r GenerationJobsRepo) MarkRunning(ctx context.Context, id uuid.UUID, startedAt time.Time) (bool, error) {
	ct, err := r.Pool.Exec(ctx, `
		update generation_jobs
		set status='running', started_at=$2
		where id=$1 and status='pending'
	`, id, startedAt)
	return ct.RowsAffected() == 1, err
}

func (r GenerationJobsRepo) MarkFinished(ctx context.Context, id uuid.UUID, status domain.JobStatus, finishedAt time.Time, totalGenerated, dupCount, avgAnswerLen int, coverageScore *float64) (bool, error) {
	ct, err := r.Pool.Exec(ctx, `
		update generation_jobs
		set status=$2,
		    finished_at=$3,
		    total_cards_generated=$4,
		    duplicate_count=$5,
		    avg_answer_length=$6,
		    coverage_score=$7
		where id=$1 and status='running'
	`, id, string(status), finishedAt, totalGenerated, dupCount, avgAnswerLen, coverageScore)
	return ct.RowsAffected() == 1, err
}

var _ usecase.GenerationJobRepository = GenerationJobsRepo{}
