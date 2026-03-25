package middleware

import (
	"net/http"
	"os"
	"path/filepath"
)

// userStorageBytes returns the total size in bytes of all files under the
// user's data directory (notes + attachments). Directories are not counted.
func userStorageBytes(dataDir, username string) int64 {
	var total int64
	filepath.Walk(filepath.Join(dataDir, username), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// QuotaCheck returns middleware that rejects POST and PUT requests with
// 507 Insufficient Storage when the authenticated user's total stored data
// has reached or exceeded their quota_bytes limit.
//
// The user is read from the request context (set by BasicAuth), so this
// middleware must be placed after BasicAuth in the chain.
func QuotaCheck(dataDir string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost && r.Method != http.MethodPut {
				next.ServeHTTP(w, r)
				return
			}

			user := UserFromContext(r.Context())
			if user == nil {
				next.ServeHTTP(w, r)
				return
			}

			if userStorageBytes(dataDir, user.Username) >= user.QuotaBytes {
				http.Error(w, "Insufficient Storage", http.StatusInsufficientStorage)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
