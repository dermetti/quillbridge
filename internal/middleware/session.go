package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/dermetti/quillbridge/internal/db"
)

const sessionCookieName = "session"

// SessionAuth returns middleware that validates a session cookie against the
// database. On success the authenticated *db.User is stored in the request
// context. On failure the client is redirected to /login.
func SessionAuth(database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			session, err := database.GetSession(cookie.Value)
			if err != nil || session == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			if time.Now().After(session.ExpiresAt) {
				// Clean up expired session.
				database.DeleteSession(cookie.Value) //nolint:errcheck
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			user, err := database.GetUserByID(session.UserID)
			if err != nil || user == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

