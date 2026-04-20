package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/usecase"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UsersRepo struct{ Pool *pgxpool.Pool }

func (r UsersRepo) Create(ctx context.Context, u domain.User) error {
	_, err := r.Pool.Exec(ctx, `
		insert into users (id, email, password_hash, name, role, created_at)
		values ($1,$2,$3,$4,$5,$6)
	`, u.ID, u.Email, u.PasswordHash, u.Name, string(u.Role), u.CreatedAt)
	return err
}

func (r UsersRepo) FindByEmail(ctx context.Context, email string) (domain.User, error) {
	row := r.Pool.QueryRow(ctx, `
		select id, email, password_hash, name, role, created_at, last_login_at
		from users where email=$1
	`, email)
	return scanUser(row)
}

func (r UsersRepo) FindByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row := r.Pool.QueryRow(ctx, `
		select id, email, password_hash, name, role, created_at, last_login_at
		from users where id=$1
	`, id)
	return scanUser(row)
}

func (r UsersRepo) UpdateLastLogin(ctx context.Context, id uuid.UUID, t time.Time) error {
	_, err := r.Pool.Exec(ctx, `update users set last_login_at=$2 where id=$1`, id, t)
	return err
}

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	var role string
	var last *time.Time
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &role, &u.CreatedAt, &last); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, pgx.ErrNoRows
		}
		return domain.User{}, err
	}
	u.Role = domain.Role(role)
	u.LastLoginAt = last
	return u, nil
}

var _ usecase.UserRepository = UsersRepo{}
