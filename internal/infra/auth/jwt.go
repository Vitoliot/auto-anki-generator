package auth

import (
	"errors"
	"time"

	"github.com/example/auto-anki/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Signer struct {
	Secret []byte
	TTL    time.Duration
}

func (s Signer) Sign(userID uuid.UUID, role domain.Role) (string, error) {
	claims := jwt.MapClaims{
		"uid":  userID.String(),
		"role": string(role),
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(s.TTL).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.Secret)
}

type ParsedToken struct {
	UserID uuid.UUID
	Role   domain.Role
}

func (s Signer) Parse(token string) (ParsedToken, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return s.Secret, nil
	})
	if err != nil || !t.Valid {
		return ParsedToken{}, errors.New("invalid token")
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return ParsedToken{}, errors.New("invalid claims")
	}
	uid, _ := claims["uid"].(string)
	role, _ := claims["role"].(string)
	id, err := uuid.Parse(uid)
	if err != nil {
		return ParsedToken{}, errors.New("invalid uid")
	}
	return ParsedToken{UserID: id, Role: domain.Role(role)}, nil
}
