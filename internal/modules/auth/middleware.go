package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
)

type ctxKey string

const userIDContextKey ctxKey = "auth_user_id"

// UserIDFromContext retrieves the authenticated user ID stored by RequireAuth.
func UserIDFromContext(ctx context.Context) (string, bool) {
	uid, ok := ctx.Value(userIDContextKey).(string)
	return uid, ok && uid != ""
}

// RequireAuth validates Bearer JWTs and injects the user ID into the request context.
func RequireAuth(tm *TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authorization header")
				return
			}

			parts := strings.SplitN(header, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
				httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authorization header format")
				return
			}

			claims, err := tm.Verify(strings.TrimSpace(parts[1]))
			if err != nil {
				httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
				return
			}

			ctx := context.WithValue(r.Context(), userIDContextKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
