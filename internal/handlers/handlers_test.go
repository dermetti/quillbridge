package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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

const testUser = "testuser"
const testPass = "testpass"

// testEnv holds the per-test HTTP server, database, and data directory.
type testEnv struct {
	server  *httptest.Server
	db      *db.DB
	dataDir string
}

// newTestEnv creates an isolated test environment with an in-memory DB,
// a temp data directory, a seeded user, and a running HTTP server.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dataDir := t.TempDir()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(testPass), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := database.CreateUser(testUser, string(hash), 104857600, false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	for _, sub := range []string{"notes", "attachments"} {
		if err := os.MkdirAll(filepath.Join(dataDir, testUser, sub), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(mw.NotesAPIVersionHeader)
		r.Use(mw.BasicAuth(database))
		r.Use(mw.QuotaCheck(dataDir))
		handlers.RegisterRoutes(r, database, dataDir)
	})

	ts := httptest.NewServer(r)
	t.Cleanup(func() {
		ts.Close()
		database.Close()
	})

	return &testEnv{server: ts, db: database, dataDir: dataDir}
}

// do performs an authenticated request and returns the response.
func (e *testEnv) do(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	var bodyR io.Reader
	if body != "" {
		bodyR = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.server.URL+path, bodyR)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.SetBasicAuth(testUser, testPass)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// doIfMatch performs a PUT with an additional If-Match header.
func (e *testEnv) doIfMatch(t *testing.T, path, body, etag string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.SetBasicAuth(testUser, testPass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", `"`+etag+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func decodeNote(t *testing.T, resp *http.Response) handlers.Note {
	t.Helper()
	defer resp.Body.Close()
	var n handlers.Note
	if err := json.NewDecoder(resp.Body).Decode(&n); err != nil {
		t.Fatalf("decode note: %v", err)
	}
	return n
}

func decodeNotes(t *testing.T, resp *http.Response) []handlers.Note {
	t.Helper()
	defer resp.Body.Close()
	var notes []handlers.Note
	if err := json.NewDecoder(resp.Body).Decode(&notes); err != nil {
		t.Fatalf("decode notes: %v", err)
	}
	return notes
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected status %d, got %d (body: %s)", want, resp.StatusCode, body)
	}
}

func noteFileExists(env *testEnv, filename string) bool {
	_, err := os.Stat(filepath.Join(env.dataDir, testUser, "notes", filename))
	return err == nil
}

// --- GET /notes ---

func TestGetNotes_Empty(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/notes", "")
	assertStatus(t, resp, http.StatusOK)

	notes := decodeNotes(t, resp)
	if len(notes) != 0 {
		t.Fatalf("expected empty list, got %d notes", len(notes))
	}
}

func TestGetNotes_ReturnsAllNotes(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Alpha","content":"a"}`)
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Beta","content":"b"}`)

	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/notes", "")
	assertStatus(t, resp, http.StatusOK)

	notes := decodeNotes(t, resp)
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
}

func TestGetNotes_CategoryFilter(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Work1","category":"work","content":"w1"}`)
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Work2","category":"work","content":"w2"}`)
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Personal","category":"personal","content":"p"}`)

	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/notes?category=work", "")
	assertStatus(t, resp, http.StatusOK)

	notes := decodeNotes(t, resp)
	if len(notes) != 2 {
		t.Fatalf("expected 2 work notes, got %d", len(notes))
	}
	for _, n := range notes {
		if n.Category != "work" {
			t.Errorf("unexpected category %q", n.Category)
		}
	}
}

func TestGetNotes_HasVersionHeader(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/notes", "")
	if got := resp.Header.Get("X-Notes-API-Versions"); got != mw.NotesAPIVersions {
		t.Errorf("expected X-Notes-API-Versions %q, got %q", mw.NotesAPIVersions, got)
	}
	resp.Body.Close()
}

// --- GET /notes/{id} ---

func TestGetNoteByID_Found(t *testing.T) {
	env := newTestEnv(t)
	created := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"FindMe","content":"hello"}`))

	resp := env.do(t, http.MethodGet, fmt.Sprintf("/apps/notes/api/v1/notes/%d", created.ID), "")
	assertStatus(t, resp, http.StatusOK)

	note := decodeNote(t, resp)
	if note.Title != "FindMe" || note.Content != "hello" {
		t.Errorf("unexpected note: %+v", note)
	}
	if note.ETag == "" {
		t.Error("expected non-empty ETag")
	}
}

func TestGetNoteByID_NotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/notes/99999", "")
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestGetNoteByID_BadID(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/notes/notanid", "")
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestGetNoteByID_ETagHeader(t *testing.T) {
	env := newTestEnv(t)
	created := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"ETagNote","content":"data"}`))

	resp := env.do(t, http.MethodGet, fmt.Sprintf("/apps/notes/api/v1/notes/%d", created.ID), "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Error("expected ETag response header")
	}
	// ETag header should be quoted.
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Errorf("ETag should be quoted, got %q", etag)
	}
}

// --- POST /notes ---

func TestCreateNote_WritesFileToDB(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"My Note","content":"body text","category":"journal","favorite":true}`)
	assertStatus(t, resp, http.StatusOK)

	note := decodeNote(t, resp)
	if note.Title != "My Note" {
		t.Errorf("expected title 'My Note', got %q", note.Title)
	}
	if note.Content != "body text" {
		t.Errorf("unexpected content %q", note.Content)
	}
	if note.Category != "journal" {
		t.Errorf("unexpected category %q", note.Category)
	}
	if !note.Favorite {
		t.Error("expected favorite=true")
	}
	if note.ID <= 0 {
		t.Errorf("expected positive ID, got %d", note.ID)
	}

	// Verify the file exists on disk.
	if !noteFileExists(env, "My Note.md") {
		t.Error("expected My Note.md to exist on disk")
	}
}

func TestCreateNote_DefaultTitle(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{}`))
	if note.Title != "New note" {
		t.Errorf("expected default title 'New note', got %q", note.Title)
	}
	if !noteFileExists(env, "New note.md") {
		t.Error("expected New note.md on disk")
	}
}

func TestCreateNote_EmptyTitleDefaultsToNewNote(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"   "}`))
	if note.Title != "New note" {
		t.Errorf("expected 'New note', got %q", note.Title)
	}
}

func TestCreateNote_TitleSanitization(t *testing.T) {
	env := newTestEnv(t)
	// Illegal chars: / \ : * ? " < > |
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"My:Note/Test"}`))
	// Colons and slashes are stripped → "MyNoteTest"
	if note.Title != "MyNoteTest" {
		t.Errorf("expected 'MyNoteTest', got %q", note.Title)
	}
	if !noteFileExists(env, "MyNoteTest.md") {
		t.Error("expected sanitized filename on disk")
	}
}

func TestCreateNote_TitleCollision(t *testing.T) {
	env := newTestEnv(t)
	n1 := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"Collision","content":"first"}`))
	n2 := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"Collision","content":"second"}`))

	if n1.Title != "Collision" {
		t.Errorf("first note title: want 'Collision', got %q", n1.Title)
	}
	if n2.Title != "Collision (2)" {
		t.Errorf("second note title: want 'Collision (2)', got %q", n2.Title)
	}

	if !noteFileExists(env, "Collision.md") {
		t.Error("expected Collision.md on disk")
	}
	if !noteFileExists(env, "Collision (2).md") {
		t.Error("expected Collision (2).md on disk")
	}
}

func TestCreateNote_ThreeWayCollision(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Triple"}`)
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Triple"}`)
	n3 := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Triple"}`))

	if n3.Title != "Triple (3)" {
		t.Errorf("expected 'Triple (3)', got %q", n3.Title)
	}
}

// --- PUT /notes/{id} ---

func TestUpdateNote_Content(t *testing.T) {
	env := newTestEnv(t)
	created := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"UpdateMe","content":"original"}`))

	resp := env.do(t, http.MethodPut,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", created.ID),
		`{"content":"updated content"}`)
	assertStatus(t, resp, http.StatusOK)

	updated := decodeNote(t, resp)
	if updated.Content != "updated content" {
		t.Errorf("expected updated content, got %q", updated.Content)
	}
	// Title should be unchanged.
	if updated.Title != "UpdateMe" {
		t.Errorf("expected title 'UpdateMe', got %q", updated.Title)
	}
	// ETag must differ from the original.
	if updated.ETag == created.ETag {
		t.Error("ETag should change after content update")
	}
}

func TestUpdateNote_TitleRename(t *testing.T) {
	env := newTestEnv(t)
	created := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"OldTitle","content":"content"}`))

	resp := env.do(t, http.MethodPut,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", created.ID),
		`{"title":"NewTitle"}`)
	assertStatus(t, resp, http.StatusOK)

	updated := decodeNote(t, resp)
	if updated.Title != "NewTitle" {
		t.Errorf("expected 'NewTitle', got %q", updated.Title)
	}

	// New file should exist; old file should be gone.
	if !noteFileExists(env, "NewTitle.md") {
		t.Error("NewTitle.md should exist")
	}
	if noteFileExists(env, "OldTitle.md") {
		t.Error("OldTitle.md should have been removed")
	}
}

func TestUpdateNote_TitleRenameCollision(t *testing.T) {
	env := newTestEnv(t)
	// Create "Target" first so the slot is occupied.
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Target"}`)
	// Create the note we'll rename.
	n := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Source"}`))

	resp := env.do(t, http.MethodPut,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", n.ID),
		`{"title":"Target"}`)
	assertStatus(t, resp, http.StatusOK)

	updated := decodeNote(t, resp)
	if updated.Title != "Target (2)" {
		t.Errorf("expected 'Target (2)', got %q", updated.Title)
	}
}

func TestUpdateNote_IfMatch_Success(t *testing.T) {
	env := newTestEnv(t)
	created := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"IfMatchOK","content":"v1"}`))

	resp := env.doIfMatch(t,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", created.ID),
		`{"content":"v2"}`,
		created.ETag)
	assertStatus(t, resp, http.StatusOK)

	updated := decodeNote(t, resp)
	if updated.Content != "v2" {
		t.Errorf("expected 'v2', got %q", updated.Content)
	}
}

func TestUpdateNote_IfMatch_Mismatch_Returns412(t *testing.T) {
	env := newTestEnv(t)
	created := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"Concurrent","content":"original"}`))

	// First client updates the note, changing the ETag.
	env.do(t, http.MethodPut,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", created.ID),
		`{"content":"changed by client A"}`)

	// Second client tries to update with the stale ETag from before the first update.
	resp := env.doIfMatch(t,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", created.ID),
		`{"content":"changed by client B"}`,
		created.ETag) // stale ETag
	assertStatus(t, resp, http.StatusPreconditionFailed)

	// Response body should be the current server state (client A's version).
	conflict := decodeNote(t, resp)
	if conflict.Content != "changed by client A" {
		t.Errorf("412 body should contain current server state, got %q", conflict.Content)
	}
}

func TestUpdateNote_NotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/apps/notes/api/v1/notes/99999", `{"content":"x"}`)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestUpdateNote_BadID(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/apps/notes/api/v1/notes/abc", `{"content":"x"}`)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

// --- DELETE /notes/{id} ---

func TestDeleteNote_RemovesFileAndRow(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"DeleteMe","content":"bye"}`))

	resp := env.do(t, http.MethodDelete,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID), "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// File should be gone.
	if noteFileExists(env, "DeleteMe.md") {
		t.Error("expected DeleteMe.md to be deleted")
	}

	// Second delete should return 404.
	resp2 := env.do(t, http.MethodDelete,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID), "")
	assertStatus(t, resp2, http.StatusNotFound)
	resp2.Body.Close()
}

func TestDeleteNote_NotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodDelete, "/apps/notes/api/v1/notes/99999", "")
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestDeleteNote_BadID(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodDelete, "/apps/notes/api/v1/notes/bad", "")
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

// --- Settings ---

func TestGetSettings(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/settings", "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var s map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s["notesPath"] != "Notes" {
		t.Errorf("expected notesPath 'Notes', got %q", s["notesPath"])
	}
	if s["fileSuffix"] != ".md" {
		t.Errorf("expected fileSuffix '.md', got %q", s["fileSuffix"])
	}
}

func TestPutSettings_ValidSuffix(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/apps/notes/api/v1/settings",
		`{"notesPath":"MyNotes","fileSuffix":".txt"}`)
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var s map[string]string
	json.NewDecoder(resp.Body).Decode(&s)
	if s["fileSuffix"] != ".txt" {
		t.Errorf("expected .txt, got %q", s["fileSuffix"])
	}
}

func TestPutSettings_InvalidSuffixDefaultsToMD(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/apps/notes/api/v1/settings",
		`{"fileSuffix":".docx"}`)
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var s map[string]string
	json.NewDecoder(resp.Body).Decode(&s)
	if s["fileSuffix"] != ".md" {
		t.Errorf("invalid suffix should fall back to .md, got %q", s["fileSuffix"])
	}
}

// --- Attachment helpers ---

// multipartBody builds a multipart/form-data body with a single "file" field.
// Returns the body bytes and the Content-Type header value (includes boundary).
func multipartBody(t *testing.T, filename string, content []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

// doUpload posts a multipart upload to the attachment endpoint for the given note.
func (e *testEnv) doUpload(t *testing.T, noteID int64, filename string, content []byte) *http.Response {
	t.Helper()
	body, ct := multipartBody(t, filename, content)
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/apps/notes/api/v1.4/attachment/%d", e.server.URL, noteID),
		bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.SetBasicAuth(testUser, testPass)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// --- POST /attachment/{id} ---

func TestUploadAttachment_SavesFile(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"AttachMe","content":"x"}`))

	imgData := []byte("fake png data")
	resp := env.doUpload(t, note.ID, "photo.png", imgData)
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	filename, ok := result["filename"]
	if !ok || filename == "" {
		t.Fatalf("expected 'filename' in response, got %v", result)
	}
	// Filename must have the original extension.
	if !strings.HasSuffix(filename, ".png") {
		t.Errorf("expected .png extension, got %q", filename)
	}
	// File must exist on disk.
	diskPath := filepath.Join(env.dataDir, testUser, "attachments", filename)
	if _, err := os.Stat(diskPath); err != nil {
		t.Errorf("expected file on disk at %s: %v", diskPath, err)
	}
}

func TestUploadAttachment_ContentAddressedFilename(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"CA"}`))

	// Uploading the same bytes twice should produce the same filename.
	data := []byte("deterministic content")
	r1 := env.doUpload(t, note.ID, "a.txt", data)
	r2 := env.doUpload(t, note.ID, "b.txt", data)
	assertStatus(t, r1, http.StatusOK)
	assertStatus(t, r2, http.StatusOK)

	var res1, res2 map[string]string
	json.NewDecoder(r1.Body).Decode(&res1)
	json.NewDecoder(r2.Body).Decode(&res2)
	r1.Body.Close()
	r2.Body.Close()

	if res1["filename"] != res2["filename"] {
		t.Errorf("same content should produce same filename: %q vs %q",
			res1["filename"], res2["filename"])
	}
}

func TestUploadAttachment_NoteNotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.doUpload(t, 99999, "x.png", []byte("data"))
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestUploadAttachment_MissingFileField(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"X"}`))

	// Send a multipart form without the "file" field.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("other", "value")
	w.Close()

	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/apps/notes/api/v1.4/attachment/%d", env.server.URL, note.ID),
		&buf)
	req.SetBasicAuth(testUser, testPass)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

// --- GET /attachment/{id}?path=... ---

func TestGetAttachment_ServesFile(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Serve"}`))

	fileContent := []byte("attachment file contents")
	uploadResp := env.doUpload(t, note.ID, "doc.txt", fileContent)
	assertStatus(t, uploadResp, http.StatusOK)
	var uploaded map[string]string
	json.NewDecoder(uploadResp.Body).Decode(&uploaded)
	uploadResp.Body.Close()

	dlPath := fmt.Sprintf("/apps/notes/api/v1.4/attachment/%d?path=%s", note.ID, uploaded["filename"])
	resp := env.do(t, http.MethodGet, dlPath, "")
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, fileContent) {
		t.Errorf("downloaded content mismatch: got %q, want %q", got, fileContent)
	}
}

func TestGetAttachment_FileNotFound(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"NoAttach"}`))

	dlPath := fmt.Sprintf("/apps/notes/api/v1.4/attachment/%d?path=nonexistent.png", note.ID)
	resp := env.do(t, http.MethodGet, dlPath, "")
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestGetAttachment_MissingPathParam(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"NP"}`))

	resp := env.do(t, http.MethodGet,
		fmt.Sprintf("/apps/notes/api/v1.4/attachment/%d", note.ID), "")
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestGetAttachment_PathTraversal(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Trav"}`))

	// Attempt directory traversal.
	dlPath := fmt.Sprintf("/apps/notes/api/v1.4/attachment/%d?path=../../etc/passwd", note.ID)
	resp := env.do(t, http.MethodGet, dlPath, "")
	// Should either 404 (file doesn't exist in attachments dir) or 400 (bad path).
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 or 404 for traversal attempt, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- Quota enforcement ---

// setQuota updates the test user's quota in the DB.
func setQuota(t *testing.T, env *testEnv, bytes int64) {
	t.Helper()
	u, err := env.db.GetUserByUsername(testUser)
	if err != nil || u == nil {
		t.Fatalf("get user: %v", err)
	}
	if err := env.db.UpdateUserQuota(u.ID, bytes); err != nil {
		t.Fatalf("set quota: %v", err)
	}
}

func TestQuota_CreateNote_507WhenExceeded(t *testing.T) {
	env := newTestEnv(t)
	// Create a note to consume some storage.
	env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Fill","content":"data"}`)
	// Drop quota to 0 so the user is now over-quota.
	setQuota(t, env, 0)

	resp := env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Blocked"}`)
	assertStatus(t, resp, http.StatusInsufficientStorage)
	resp.Body.Close()
}

func TestQuota_UpdateNote_507WhenExceeded(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"EditMe","content":"original"}`))
	setQuota(t, env, 0)

	resp := env.do(t, http.MethodPut,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID),
		`{"content":"updated"}`)
	assertStatus(t, resp, http.StatusInsufficientStorage)
	resp.Body.Close()
}

func TestQuota_UploadAttachment_507WhenExceeded(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"Q"}`))
	setQuota(t, env, 0)

	resp := env.doUpload(t, note.ID, "img.png", []byte("some bytes"))
	assertStatus(t, resp, http.StatusInsufficientStorage)
	resp.Body.Close()
}

func TestQuota_GetIsAlwaysAllowed(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"ReadMe","content":"hi"}`))
	setQuota(t, env, 0)

	// GETs must not be blocked by quota.
	resp := env.do(t, http.MethodGet, "/apps/notes/api/v1/notes", "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = env.do(t, http.MethodGet,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID), "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestQuota_DeleteIsAlwaysAllowed(t *testing.T) {
	env := newTestEnv(t)
	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"DelQ","content":"x"}`))
	setQuota(t, env, 0)

	// DELETE must not be blocked by quota (user should be able to free space).
	resp := env.do(t, http.MethodDelete,
		fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID), "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// --- X-Notes-API-Versions header on all endpoints ---

func TestAllEndpoints_HaveVersionHeader(t *testing.T) {
	env := newTestEnv(t)

	note := decodeNote(t, env.do(t, http.MethodPost, "/apps/notes/api/v1/notes",
		`{"title":"HeaderCheck","content":"x"}`))

	paths := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/apps/notes/api/v1/notes", ""},
		{http.MethodGet, fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID), ""},
		{http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"H2"}`},
		{http.MethodPut, fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID), `{"content":"y"}`},
		{http.MethodDelete, fmt.Sprintf("/apps/notes/api/v1/notes/%d", note.ID), ""},
		{http.MethodGet, "/apps/notes/api/v1/settings", ""},
		{http.MethodPut, "/apps/notes/api/v1/settings", `{"fileSuffix":".md"}`},
	}
	for _, tc := range paths {
		resp := env.do(t, tc.method, tc.path, tc.body)
		resp.Body.Close()
		if got := resp.Header.Get("X-Notes-API-Versions"); got != mw.NotesAPIVersions {
			t.Errorf("%s %s: expected X-Notes-API-Versions %q, got %q",
				tc.method, tc.path, mw.NotesAPIVersions, got)
		}
	}
}

// TestVersionHeader_On401 ensures the X-Notes-API-Versions header is present
// even when BasicAuth rejects the request with 401.
func TestVersionHeader_On401(t *testing.T) {
	env := newTestEnv(t)
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/apps/notes/api/v1/notes", nil)
	req.SetBasicAuth("nobody", "wrongpass")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Notes-API-Versions"); got != mw.NotesAPIVersions {
		t.Errorf("401 response missing X-Notes-API-Versions: got %q", got)
	}
}

// TestVersionHeader_On507 ensures the X-Notes-API-Versions header is present
// when QuotaCheck rejects a POST with 507.
func TestVersionHeader_On507(t *testing.T) {
	env := newTestEnv(t)
	// Set quota to 0 so the very next POST triggers 507.
	if err := env.db.UpdateUserQuota(1, 0); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	resp := env.do(t, http.MethodPost, "/apps/notes/api/v1/notes", `{"title":"q"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("expected 507, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Notes-API-Versions"); got != mw.NotesAPIVersions {
		t.Errorf("507 response missing X-Notes-API-Versions: got %q", got)
	}
}
