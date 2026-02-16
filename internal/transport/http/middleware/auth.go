package middleware

import (
	"net/http"
	"strings"

	"github.com/example/auto-anki/internal/domain"
	"github.com/example/auto-anki/internal/infra/auth"
	"github.com/example/auto-anki/internal/transport/httpctx"
)

type AuthMiddleware struct {
	Signer auth.Signer
}

func (m AuthMiddleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if h == "" || !strings.HasPrefix(h, "Bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(h, "Bearer ")
		pt, err := m.Signer.Parse(token)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, httpctx.WithAuth(r, pt.UserID, pt.Role))
	})
}

func (m AuthMiddleware) RequireRole(role domain.Role, next http.Handler) http.Handler {
	return m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := httpctx.Role(r.Context())
		if got != role {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
