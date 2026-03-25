package middleware

import "net/http"

// NotesAPIVersions is the value of the X-Notes-API-Versions header required
// by every Notes API response per the Nextcloud Notes API specification.
const NotesAPIVersions = "1.0, 1.1, 1.2, 1.3, 1.4"

// NotesAPIVersionHeader injects the X-Notes-API-Versions header into every
// response, as required by the Nextcloud Notes API specification.
func NotesAPIVersionHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Notes-API-Versions", NotesAPIVersions)
		next.ServeHTTP(w, r)
	})
}
