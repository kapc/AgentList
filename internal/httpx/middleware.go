package httpx

import (
	"context"
	"net/http"

	"github.com/chandresh/agentlist/internal/auth"
	"github.com/chandresh/agentlist/internal/store"
)

type contextKey string

const userContextKey contextKey = "user"

func AuthMiddleware(tokens *auth.TokenManager, lookup func(string) (store.User, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(auth.CookieName())
			if err != nil || cookie.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			claims, err := tokens.Parse(cookie.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			user, err := lookup(claims.UserID)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := userFromContext(r.Context()); !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func userFromContext(ctx context.Context) (store.User, bool) {
	user, ok := ctx.Value(userContextKey).(store.User)
	return user, ok
}
