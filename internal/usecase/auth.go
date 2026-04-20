package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailTaken         = errors.New("email already taken")
)

type TokenSigner interface {
	Sign(userID uuid.UUID, role domain.Role) (string, error)
}

type AuthService struct {
	Users  UserRepository
	Clock  Clock
	Signer TokenSigner
}

func (s AuthService) Register(ctx context.Context, email, password, name string) (domain.User, string, error) {
	_, err := s.Users.FindByEmail(ctx, email)
	if err == nil {
		return domain.User{}, "", ErrEmailTaken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, "", err
	}

	u := domain.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: string(hash),
		Name:         name,
		Role:         domain.RoleUser,
		CreatedAt:    s.Clock.Now(),
	}

	if err := s.Users.Create(ctx, u); err != nil {
		return domain.User{}, "", err
	}

	token, err := s.Signer.Sign(u.ID, u.Role)
	if err != nil {
		return domain.User{}, "", err
	}

	return u, token, nil
}

func (s AuthService) Login(ctx context.Context, email, password string) (domain.User, string, error) {
	u, err := s.Users.FindByEmail(ctx, email)
	if err != nil {
		return domain.User{}, "", ErrInvalidCredentials
	}

	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return domain.User{}, "", ErrInvalidCredentials
	}

	now := s.Clock.Now()
	_ = s.Users.UpdateLastLogin(ctx, u.ID, now)

	token, err := s.Signer.Sign(u.ID, u.Role)
	if err != nil {
		return domain.User{}, "", err
	}

	return u, token, nil
}

// NOTE: Password policy/validation intentionally minimal for prototype.
func ValidatePassword(pw string) error {
	if len(pw) < 8 {
		return errors.New("password must be at least 8 chars")
	}
	return nil
}

func ValidateEmail(email string) error {
	// Minimal; в проде — нормальный парсер/валидатор.
	if len(email) < 5 {
		return errors.New("invalid email")
	}
	return nil
}

func ValidateName(name string) error {
	if len(name) < 2 {
		return errors.New("name too short")
	}
	return nil
}

func (s AuthService) PromoteToAdmin(ctx context.Context, userID uuid.UUID) error {
	// intentionally omitted in prototype
	_ = userID
	return errors.New("not implemented")
}

type tokenClaims struct {
	UserID string `json:"uid"`
	Role   string `json:"role"`
	Iat    int64  `json:"iat"`
	Exp    int64  `json:"exp"`
	_      struct{}
}

func tokenTTL() time.Duration { return 24 * time.Hour }
