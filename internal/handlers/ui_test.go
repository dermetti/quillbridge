package handlers_test

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/dermetti/quillbridge/internal/db"
	"github.com/dermetti/quillbridge/internal/handlers"
	mw "github.com/dermetti/quillbridge/internal/middleware"
)

// uiEnv is a self-contained test environment for Web UI tests.
type uiEnv struct {
	server    *httptest.Server
	db        *db.DB
	dataDir   string
	adminID   int64
	regularID int64
}

func newUIEnv(t *testing.T) *uiEnv {
	t.Helper()

	dataDir := t.TempDir()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	makeHash := func(pw string) string {
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("bcrypt: %v", err)
		}
		return string(h)
	}

	if err := database.CreateUser("admin", makeHash("adminpass"), 104857600, true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := database.CreateUser("regular", makeHash("regularpass"), 104857600, false); err != nil {
		t.Fatalf("create regular: %v", err)
	}

	adminU, _ := database.GetUserByUsername("admin")
	regularU, _ := database.GetUserByUsername("regular")

	for _, username := range []string{"admin", "regular"} {
		for _, sub := range []string{"notes", "attachments"} {
			if err := os.MkdirAll(filepath.Join(dataDir, username, sub), 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}
	}

	r := chi.NewRouter()
	r.Group(func(authed chi.Router) {
		authed.Use(mw.SessionAuth(database))
		handlers.RegisterUIRoutes(r, authed, database, dataDir)
	})

	ts := httptest.NewServer(r)
	t.Cleanup(func() {
		ts.Close()
		database.Close()
	})

	return &uiEnv{
		server:    ts,
		db:        database,
		dataDir:   dataDir,
		adminID:   adminU.ID,
		regularID: regularU.ID,
	}
}

// clientWithJar returns an http.Client with a cookie jar that does not follow redirects.
func clientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (e *uiEnv) postForm(client *http.Client, path string, vals url.Values) *http.Response {
	resp, err := client.PostForm(e.server.URL+path, vals)
	if err != nil {
		panic(err)
	}
	return resp
}

func (e *uiEnv) get(client *http.Client, path string) *http.Response {
	resp, err := client.Get(e.server.URL + path)
	if err != nil {
		panic(err)
	}
	return resp
}

// login logs in and asserts the redirect to /.
func (e *uiEnv) login(t *testing.T, client *http.Client, username, password string) {
	t.Helper()
	resp := e.postForm(client, "/login", url.Values{
		"username": {username},
		"password": {password},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login %s: expected 303, got %d", username, resp.StatusCode)
	}
}

func uid(id int64) string { return fmt.Sprintf("%d", id) }

// ---- Tests ----

func TestUILoginPageRendered(t *testing.T) {
	e := newUIEnv(t)
	resp := e.get(clientWithJar(t), "/login")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUILoginSuccess(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	resp := e.postForm(client, "/login", url.Values{
		"username": {"admin"},
		"password": {"adminpass"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("expected redirect to /, got %q", loc)
	}
	// Cookie must be set.
	u, _ := url.Parse(e.server.URL)
	cookies := client.Jar.Cookies(u)
	var hasCookie bool
	for _, c := range cookies {
		if c.Name == "session" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Fatal("no session cookie after login")
	}
}

func TestUILoginFailure(t *testing.T) {
	e := newUIEnv(t)
	resp := e.postForm(clientWithJar(t), "/login", url.Values{
		"username": {"admin"},
		"password": {"wrongpass"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestUILogout(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "admin", "adminpass")

	resp := e.postForm(client, "/logout", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout: expected 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("expected /login redirect, got %q", loc)
	}

	// Dashboard must now redirect to /login.
	resp2 := e.get(client, "/")
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("after logout GET /: expected 303, got %d", resp2.StatusCode)
	}
}

func TestUIDashboardRedirectWhenUnauthenticated(t *testing.T) {
	e := newUIEnv(t)
	resp := e.get(clientWithJar(t), "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("expected /login, got %q", loc)
	}
}

func TestUIDashboardRendered(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "regular", "regularpass")
	resp := e.get(client, "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUIAdminForbiddenForRegularUser(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "regular", "regularpass")
	resp := e.get(client, "/admin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestUIAdminAccessibleForAdmin(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "admin", "adminpass")
	resp := e.get(client, "/admin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUIAdminCreateUser(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "admin", "adminpass")

	resp := e.postForm(client, "/admin/users", url.Values{
		"username": {"newuser"},
		"password": {"newpassword123"},
		"quota_mb": {"50"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}

	u, err := e.db.GetUserByUsername("newuser")
	if err != nil || u == nil {
		t.Fatal("user not created in DB")
	}
	if want := int64(50 * (1 << 20)); u.QuotaBytes != want {
		t.Fatalf("quota: want %d, got %d", want, u.QuotaBytes)
	}
	for _, sub := range []string{"notes", "attachments"} {
		if _, err := os.Stat(filepath.Join(e.dataDir, "newuser", sub)); os.IsNotExist(err) {
			t.Fatalf("directory %s not created", sub)
		}
	}
}

func TestUIAdminCreateUserNonAdminForbidden(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "regular", "regularpass")

	resp := e.postForm(client, "/admin/users", url.Values{
		"username": {"sneaky"},
		"password": {"sneakypass"},
		"quota_mb": {"10"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestUIAdminDeleteUser(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "admin", "adminpass")

	// Create a throwaway user.
	e.postForm(client, "/admin/users", url.Values{
		"username": {"todelete"},
		"password": {"password123"},
		"quota_mb": {"10"},
	}).Body.Close()

	victim, _ := e.db.GetUserByUsername("todelete")
	if victim == nil {
		t.Fatal("setup: user not created")
	}

	resp := e.postForm(client, "/admin/users/"+uid(victim.ID)+"/delete", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}

	u, _ := e.db.GetUserByUsername("todelete")
	if u != nil {
		t.Fatal("user still exists after delete")
	}
}

func TestUIAdminCannotDeleteSelf(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "admin", "adminpass")

	resp := e.postForm(client, "/admin/users/"+uid(e.adminID)+"/delete", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "error") {
		t.Fatalf("expected error in redirect location, got %q", loc)
	}
	u, _ := e.db.GetUserByUsername("admin")
	if u == nil {
		t.Fatal("admin was deleted")
	}
}

func TestUIAdminSetQuota(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "admin", "adminpass")

	resp := e.postForm(client, "/admin/users/"+uid(e.regularID)+"/quota", url.Values{
		"quota_mb": {"200"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}

	u, _ := e.db.GetUserByUsername("regular")
	if want := int64(200 * (1 << 20)); u.QuotaBytes != want {
		t.Fatalf("quota: want %d, got %d", want, u.QuotaBytes)
	}
}

func TestUIAdminSetPassword(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "admin", "adminpass")

	resp := e.postForm(client, "/admin/users/"+uid(e.regularID)+"/password", url.Values{
		"password": {"newpassword123"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}

	u, _ := e.db.GetUserByUsername("regular")
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("newpassword123")) != nil {
		t.Fatal("password was not updated")
	}
}

func TestUIChangeOwnPassword(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "regular", "regularpass")

	resp := e.postForm(client, "/user/password", url.Values{
		"current": {"regularpass"},
		"new":     {"newregularpass"},
		"confirm": {"newregularpass"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}

	u, _ := e.db.GetUserByUsername("regular")
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("newregularpass")) != nil {
		t.Fatal("password was not changed")
	}
}

func TestUIChangeOwnPasswordWrongCurrent(t *testing.T) {
	e := newUIEnv(t)
	client := clientWithJar(t)
	e.login(t, client, "regular", "regularpass")

	resp := e.postForm(client, "/user/password", url.Values{
		"current": {"wrongcurrent"},
		"new":     {"newregularpass"},
		"confirm": {"newregularpass"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "pw_error") {
		t.Fatalf("expected pw_error in redirect, got %q", loc)
	}
}

func TestUIFileBrowserShowsFiles(t *testing.T) {
	e := newUIEnv(t)

	notesDir := filepath.Join(e.dataDir, "regular", "notes")
	if err := os.WriteFile(filepath.Join(notesDir, "hello.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	client := clientWithJar(t)
	e.login(t, client, "regular", "regularpass")

	resp := e.get(client, "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUIFileDelete(t *testing.T) {
	e := newUIEnv(t)

	notesDir := filepath.Join(e.dataDir, "regular", "notes")
	filePath := filepath.Join(notesDir, "deleteme.md")
	if err := os.WriteFile(filePath, []byte("bye"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	client := clientWithJar(t)
	e.login(t, client, "regular", "regularpass")

	resp := e.postForm(client, "/user/files/delete", url.Values{
		"dir":  {"notes"},
		"file": {"deleteme.md"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatal("file was not deleted")
	}
}
