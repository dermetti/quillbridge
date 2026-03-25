package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/dermetti/quillbridge/internal/db"
)

// --- PathScrubber tests ---

func TestPathScrubber_IndexPHP(t *testing.T) {
	recorded := ""
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded = r.URL.Path
	})

	r := httptest.NewRequest(http.MethodGet, "/index.php/apps/notes/api/v1/notes", nil)
	PathScrubber(next).ServeHTTP(httptest.NewRecorder(), r)

	if recorded != "/apps/notes/api/v1/notes" {
		t.Errorf("expected /apps/notes/api/v1/notes, got %q", recorded)
	}
}

func TestPathScrubber_RemotePHP(t *testing.T) {
	recorded := ""
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded = r.URL.Path
	})

	r := httptest.NewRequest(http.MethodGet, "/remote.php/dav/notes", nil)
	PathScrubber(next).ServeHTTP(httptest.NewRecorder(), r)

	if recorded != "/dav/notes" {
		t.Errorf("expected /dav/notes, got %q", recorded)
	}
}

func TestPathScrubber_IndexPHP_RootOnly(t *testing.T) {
	// /index.php with nothing after it should become /
	recorded := ""
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded = r.URL.Path
	})

	r := httptest.NewRequest(http.MethodGet, "/index.php", nil)
	PathScrubber(next).ServeHTTP(httptest.NewRecorder(), r)

	if recorded != "/" {
		t.Errorf("expected /, got %q", recorded)
	}
}

func TestPathScrubber_Passthrough(t *testing.T) {
	recorded := ""
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded = r.URL.Path
	})

	r := httptest.NewRequest(http.MethodGet, "/apps/notes/api/v1/notes", nil)
	PathScrubber(next).ServeHTTP(httptest.NewRecorder(), r)

	if recorded != "/apps/notes/api/v1/notes" {
		t.Errorf("expected path unchanged, got %q", recorded)
	}
}

func TestPathScrubber_UnrelatedPrefix(t *testing.T) {
	recorded := ""
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded = r.URL.Path
	})

	r := httptest.NewRequest(http.MethodGet, "/ocs/v2.php/cloud/capabilities", nil)
	PathScrubber(next).ServeHTTP(httptest.NewRecorder(), r)

	if recorded != "/ocs/v2.php/cloud/capabilities" {
		t.Errorf("expected path unchanged, got %q", recorded)
	}
}

// --- BasicAuth tests ---

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func seedUser(t *testing.T, database *db.DB, username, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := database.CreateUser(username, string(hash), 104857600, false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
}

func TestBasicAuth_NoCredentials(t *testing.T) {
	database := openTestDB(t)
	handler := BasicAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header")
	}
}

func TestBasicAuth_WrongPassword(t *testing.T) {
	database := openTestDB(t)
	seedUser(t, database, "alice", "correctpass")

	handler := BasicAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("alice", "wrongpass")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestBasicAuth_UnknownUser(t *testing.T) {
	database := openTestDB(t)

	handler := BasicAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("nobody", "pass")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestBasicAuth_ValidCredentials_PassesThrough(t *testing.T) {
	database := openTestDB(t)
	seedUser(t, database, "bob", "secret")

	var ctxUser interface{}
	handler := BasicAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxUser = r.Context().Value(userContextKey)
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("bob", "secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ctxUser == nil {
		t.Error("expected user in context, got nil")
	}
}

func TestBasicAuth_UserFromContext(t *testing.T) {
	database := openTestDB(t)
	seedUser(t, database, "carol", "pass123")

	var gotUser *db.User
	handler := BasicAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetBasicAuth("carol", "pass123")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if gotUser == nil {
		t.Fatal("UserFromContext returned nil")
	}
	if gotUser.Username != "carol" {
		t.Errorf("expected carol, got %s", gotUser.Username)
	}
}

func TestUserFromContext_NilWhenAbsent(t *testing.T) {
	u := UserFromContext(context.Background())
	if u != nil {
		t.Errorf("expected nil, got %+v", u)
	}
}

// --- NotesAPIVersionHeader tests ---

func TestNotesAPIVersionHeader(t *testing.T) {
	handler := NotesAPIVersionHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	got := w.Header().Get("X-Notes-API-Versions")
	if got != NotesAPIVersions {
		t.Errorf("expected %q, got %q", NotesAPIVersions, got)
	}
}

// --- SessionAuth tests ---

func TestSessionAuth_NoCookie_Redirects(t *testing.T) {
	database := openTestDB(t)

	handler := SessionAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	if w.Header().Get("Location") != "/login" {
		t.Errorf("expected redirect to /login, got %q", w.Header().Get("Location"))
	}
}

func TestSessionAuth_InvalidToken_Redirects(t *testing.T) {
	database := openTestDB(t)

	handler := SessionAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "badtoken"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
}

func TestSessionAuth_ExpiredSession_Redirects(t *testing.T) {
	database := openTestDB(t)
	seedUser(t, database, "dave", "pass")
	u, _ := database.GetUserByUsername("dave")
	database.CreateSession("expiredtok", u.ID, time.Now().Add(-time.Hour))

	handler := SessionAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "expiredtok"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
}

func TestSessionAuth_ValidSession_PassesThrough(t *testing.T) {
	database := openTestDB(t)
	seedUser(t, database, "eve", "pass")
	u, _ := database.GetUserByUsername("eve")
	database.CreateSession("validtok", u.ID, time.Now().Add(time.Hour))

	var gotUser *db.User
	handler := SessionAuth(database)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "validtok"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if gotUser == nil || gotUser.Username != "eve" {
		t.Errorf("expected user eve in context, got %v", gotUser)
	}
}
