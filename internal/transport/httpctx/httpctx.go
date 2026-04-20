package httpctx

import (
	"context"
	"net/http"

	"github.com/example/auto-anki/internal/domain"
	"github.com/google/uuid"
)

type ctxKey string

const (
	keyUserID ctxKey = "userID"
	keyRole   ctxKey = "role"
)

func WithAuth(r *http.Request, userID uuid.UUID, role domain.Role) *http.Request {
	ctx := context.WithValue(r.Context(), keyUserID, userID)
	ctx = context.WithValue(ctx, keyRole, role)
	return r.WithContext(ctx)
}

func UserID(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(keyUserID).(uuid.UUID)
	return v, ok
}

func Role(ctx context.Context) (domain.Role, bool) {
	v, ok := ctx.Value(keyRole).(domain.Role)
	return v, ok
}
