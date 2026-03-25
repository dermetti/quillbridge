package middleware

import (
	"context"
	"net/http"

	"github.com/dermetti/quillbridge/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// contextKey is an unexported type for context keys in this package,
// preventing collisions with keys defined in other packages.
type contextKey int

const userContextKey contextKey = iota

// BasicAuth returns middleware that validates HTTP Basic Auth credentials
// against the database. On success the authenticated *db.User is stored in
// the request context. On failure a 401 is returned.
func BasicAuth(database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()
			if !ok {
				writeUnauthorized(w)
				return
			}

			user, err := database.GetUserByUsername(username)
			if err != nil || user == nil {
				writeUnauthorized(w)
				return
			}

			if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
				writeUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext retrieves the authenticated *db.User stored by BasicAuth or
// SessionAuth. Returns nil if no user is present.
func UserFromContext(ctx context.Context) *db.User {
	u, _ := ctx.Value(userContextKey).(*db.User)
	return u
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Quillbridge"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
