package middleware

import (
	"net/http"
	"strings"
)

// PathScrubber strips /index.php and /remote.php prefixes from the request
// path so the router can match Nextcloud-style URLs sent by Quillpad.
func PathScrubber(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, prefix := range []string{"/index.php", "/remote.php"} {
			if strings.HasPrefix(r.URL.Path, prefix) {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}
