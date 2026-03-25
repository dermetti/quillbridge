package handlers

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	mw "github.com/dermetti/quillbridge/internal/middleware"
)

//go:embed templates/*.html
var templateFS embed.FS

var tmpl = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// formatBytes returns a human-readable size string (KB / MB).
func formatBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// generateToken returns a 32-byte random hex string for session tokens.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// fileEntry is used in dashboard template.
type fileEntry struct {
	Name      string
	SizeHuman string
}

func listFiles(dir string) []fileEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []fileEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, fileEntry{Name: e.Name(), SizeHuman: formatBytes(info.Size())})
	}
	return out
}

// --- Login ---

func (h *Handler) getLogin(w http.ResponseWriter, r *http.Request) {
	tmpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": ""}) //nolint:errcheck
}

func (h *Handler) postLogin(w http.ResponseWriter, r *http.Request) {
	renderErr := func(msg string) {
		w.WriteHeader(http.StatusUnauthorized)
		tmpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": msg}) //nolint:errcheck
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := h.db.GetUserByUsername(username)
	if err != nil || user == nil {
		renderErr("Invalid username or password.")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		renderErr("Invalid username or password.")
		return
	}

	token, err := generateToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expires := time.Now().Add(24 * time.Hour)
	if err := h.db.CreateSession(token, user.ID, expires); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Logout ---

func (h *Handler) postLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session"); err == nil {
		h.db.DeleteSession(cookie.Value) //nolint:errcheck
	}
	http.SetCookie(w, &http.Cookie{
		Name:    "session",
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- Dashboard ---

func (h *Handler) getDashboard(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())
	used := userStorageBytes(h.dataDir, user.Username)

	data := map[string]any{
		"User":        user,
		"UsedBytes":   used,
		"UsedHuman":   formatBytes(used),
		"QuotaHuman":  formatBytes(user.QuotaBytes),
		"Notes":       listFiles(filepath.Join(h.dataDir, user.Username, "notes")),
		"Attachments": listFiles(filepath.Join(h.dataDir, user.Username, "attachments")),
		"PwError":     r.URL.Query().Get("pw_error"),
		"PwOK":        r.URL.Query().Get("pw_ok") == "1",
		"UploadError": r.URL.Query().Get("upload_error"),
	}
	tmpl.ExecuteTemplate(w, "dashboard.html", data) //nolint:errcheck
}

// --- Change own password ---

func (h *Handler) postUserPassword(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())
	current := r.FormValue("current")
	newPw := r.FormValue("new")
	confirm := r.FormValue("confirm")

	redirectErr := func(msg string) {
		http.Redirect(w, r, "/?pw_error="+msg, http.StatusSeeOther)
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)) != nil {
		redirectErr("Current+password+is+incorrect.")
		return
	}
	if newPw != confirm {
		redirectErr("Passwords+do+not+match.")
		return
	}
	if len(newPw) < 8 {
		redirectErr("Password+must+be+at+least+8+characters.")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.db.UpdateUserPassword(user.ID, string(hash)); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?pw_ok=1", http.StatusSeeOther)
}

// --- File upload / delete ---

func (h *Handler) postUserFileUpload(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	redirectErr := func(msg string) {
		http.Redirect(w, r, "/?upload_error="+msg, http.StatusSeeOther)
	}

	dir := r.FormValue("dir")
	if dir != "notes" && dir != "attachments" {
		redirectErr("Invalid+directory.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		redirectErr("File+too+large.")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		redirectErr("Missing+file.")
		return
	}
	defer file.Close()

	// Sanitize filename.
	name := filepath.Base(header.Filename)
	if name == "." || name == ".." {
		redirectErr("Invalid+filename.")
		return
	}

	dest := filepath.Join(h.dataDir, user.Username, dir, name)
	out, err := os.Create(dest)
	if err != nil {
		redirectErr("Could+not+save+file.")
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		redirectErr("Could+not+save+file.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) postUserFileDelete(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	dir := r.FormValue("dir")
	if dir != "notes" && dir != "attachments" {
		http.Error(w, "invalid directory", http.StatusBadRequest)
		return
	}
	name := filepath.Base(r.FormValue("file"))
	if name == "." || name == ".." || name == "" {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	os.Remove(filepath.Join(h.dataDir, user.Username, dir, name)) //nolint:errcheck
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Admin dashboard ---

type adminUserRow struct {
	ID         int64
	Username   string
	IsAdmin    bool
	QuotaHuman string
	QuotaMB    int64
	UsedHuman  string
}

func (h *Handler) getAdmin(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())
	if !user.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, err := h.db.GetAllUsers()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rows := make([]adminUserRow, 0, len(users))
	for _, u := range users {
		used := userStorageBytes(h.dataDir, u.Username)
		rows = append(rows, adminUserRow{
			ID:         u.ID,
			Username:   u.Username,
			IsAdmin:    u.IsAdmin,
			QuotaHuman: formatBytes(u.QuotaBytes),
			QuotaMB:    u.QuotaBytes / (1 << 20),
			UsedHuman:  formatBytes(used),
		})
	}

	data := map[string]any{
		"Users": rows,
		"Error": r.URL.Query().Get("error"),
		"OK":    r.URL.Query().Get("ok"),
	}
	tmpl.ExecuteTemplate(w, "admin.html", data) //nolint:errcheck
}

// --- Admin: create user ---

func (h *Handler) postAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())
	if !user.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	quotaMBStr := r.FormValue("quota_mb")
	isAdmin := r.FormValue("is_admin") == "1"

	quotaMB, err := strconv.ParseInt(quotaMBStr, 10, 64)
	if err != nil || quotaMB < 1 {
		http.Redirect(w, r, "/admin?error=Invalid+quota.", http.StatusSeeOther)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.db.CreateUser(username, string(hash), quotaMB*(1<<20), isAdmin); err != nil {
		http.Redirect(w, r, "/admin?error=Could+not+create+user+(username+taken%3F)", http.StatusSeeOther)
		return
	}

	for _, sub := range []string{"notes", "attachments"} {
		os.MkdirAll(filepath.Join(h.dataDir, username, sub), 0755) //nolint:errcheck
	}

	http.Redirect(w, r, "/admin?ok=User+created.", http.StatusSeeOther)
}

// --- Admin: delete user ---

func (h *Handler) postAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := mw.UserFromContext(r.Context())
	if !actor.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	targetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin?error=Invalid+user+ID.", http.StatusSeeOther)
		return
	}
	if targetID == actor.ID {
		http.Redirect(w, r, "/admin?error=Cannot+delete+yourself.", http.StatusSeeOther)
		return
	}

	target, err := h.db.GetUserByID(targetID)
	if err != nil || target == nil {
		http.Redirect(w, r, "/admin?error=User+not+found.", http.StatusSeeOther)
		return
	}

	if err := h.db.DeleteUser(targetID); err != nil {
		http.Redirect(w, r, "/admin?error=Could+not+delete+user.", http.StatusSeeOther)
		return
	}
	os.RemoveAll(filepath.Join(h.dataDir, target.Username)) //nolint:errcheck

	http.Redirect(w, r, "/admin?ok=User+deleted.", http.StatusSeeOther)
}

// --- Admin: set quota ---

func (h *Handler) postAdminSetQuota(w http.ResponseWriter, r *http.Request) {
	actor := mw.UserFromContext(r.Context())
	if !actor.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	targetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin?error=Invalid+user+ID.", http.StatusSeeOther)
		return
	}

	quotaMB, err := strconv.ParseInt(r.FormValue("quota_mb"), 10, 64)
	if err != nil || quotaMB < 1 {
		http.Redirect(w, r, "/admin?error=Invalid+quota.", http.StatusSeeOther)
		return
	}

	if err := h.db.UpdateUserQuota(targetID, quotaMB*(1<<20)); err != nil {
		http.Redirect(w, r, "/admin?error=Could+not+update+quota.", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin?ok=Quota+updated.", http.StatusSeeOther)
}

// --- Admin: reset user password ---

func (h *Handler) postAdminSetPassword(w http.ResponseWriter, r *http.Request) {
	actor := mw.UserFromContext(r.Context())
	if !actor.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	targetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/admin?error=Invalid+user+ID.", http.StatusSeeOther)
		return
	}

	password := r.FormValue("password")
	if len(password) < 8 {
		http.Redirect(w, r, "/admin?error=Password+must+be+at+least+8+characters.", http.StatusSeeOther)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.db.UpdateUserPassword(targetID, string(hash)); err != nil {
		http.Redirect(w, r, "/admin?error=Could+not+update+password.", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin?ok=Password+updated.", http.StatusSeeOther)
}

// userStorageBytes is imported from quota.go in the same package.
// Re-declared here to avoid import cycle — actually it's in middleware, so
// we call the helper directly using filepath.Walk.
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
