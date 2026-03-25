package handlers

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dermetti/quillbridge/internal/db"
	mw "github.com/dermetti/quillbridge/internal/middleware"
)

// Note is the JSON shape returned by every Notes API endpoint.
// Field names and types match the Nextcloud Notes API v1 spec exactly.
type Note struct {
	ID       int64  `json:"id"`
	ETag     string `json:"etag"`
	Readonly bool   `json:"readonly"`
	Content  string `json:"content"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Favorite bool   `json:"favorite"`
	Modified int64  `json:"modified"`
}

// noteRequest decodes POST/PUT bodies. Pointer fields let us distinguish
// "field absent" from "field set to zero value".
type noteRequest struct {
	Title    *string `json:"title"`
	Category *string `json:"category"`
	Content  *string `json:"content"`
	Favorite *bool   `json:"favorite"`
	Modified *int64  `json:"modified"`
}

// appSettings mirrors the Nextcloud Notes settings payload.
type appSettings struct {
	NotesPath  string `json:"notesPath"`
	FileSuffix string `json:"fileSuffix"`
}

// Handler holds shared dependencies for every HTTP handler.
type Handler struct {
	db      *db.DB
	dataDir string
}

// New creates a Handler backed by the given database and data directory.
func New(database *db.DB, dataDir string) *Handler {
	return &Handler{db: database, dataDir: dataDir}
}

func (h *Handler) notesDir(username string) string {
	return filepath.Join(h.dataDir, username, "notes")
}

// noteToResponse builds a Note from a DB row and the note's file content.
func noteToResponse(meta *db.NoteMeta, content string) Note {
	// Title is the filename without its extension.
	title := strings.TrimSuffix(meta.Filename, filepath.Ext(meta.Filename))
	return Note{
		ID:       meta.ID,
		ETag:     computeEtag(content),
		Readonly: false,
		Content:  content,
		Title:    title,
		Category: meta.Category,
		Favorite: meta.IsFavorite,
		Modified: meta.ModifiedAt,
	}
}

// computeEtag returns the MD5 hex digest of content, matching Nextcloud's etag scheme.
func computeEtag(content string) string {
	sum := md5.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// sanitizeTitle removes characters that are illegal in filenames across common
// operating systems, then trims surrounding whitespace. Falls back to
// "New note" if the sanitised result is empty.
func sanitizeTitle(title string) string {
	const illegal = `/\:*?"<>|`
	var b strings.Builder
	for _, ch := range title {
		if !strings.ContainsRune(illegal, ch) {
			b.WriteRune(ch)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "New note"
	}
	return s
}

// uniqueFilename returns a .md filename for title that does not already exist
// in dir, appending " (2)", " (3)", … until a free slot is found.
func uniqueFilename(dir, title string) string {
	candidate := title + ".md"
	if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
		return candidate
	}
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s (%d).md", title, i)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

// uniqueFilenameExcluding is like uniqueFilename but treats excludeFilename as
// available. This is used during PUT when the old file is about to be freed by
// a rename, preventing the old slot from being incorrectly counted as occupied.
func uniqueFilenameExcluding(dir, title, excludeFilename string) string {
	candidate := title + ".md"
	if candidate == excludeFilename {
		return candidate
	}
	if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
		return candidate
	}
	for i := 2; ; i++ {
		candidate = fmt.Sprintf("%s (%d).md", title, i)
		if candidate == excludeFilename {
			// This slot is the file we are renaming away from; treat it as free.
			return candidate
		}
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// --- Notes handlers ---

func (h *Handler) getNotes(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	metas, err := h.db.GetAllNotesMeta(user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	categoryFilter := r.URL.Query().Get("category")
	notesDir := h.notesDir(user.Username)

	notes := make([]Note, 0, len(metas))
	for _, meta := range metas {
		if categoryFilter != "" && meta.Category != categoryFilter {
			continue
		}
		content, err := os.ReadFile(filepath.Join(notesDir, meta.Filename))
		if err != nil {
			// File missing — skip rather than fail the whole list.
			continue
		}
		notes = append(notes, noteToResponse(meta, string(content)))
	}

	// Compute a list-level ETag as the MD5 of all individual ETags.
	var sb strings.Builder
	for _, n := range notes {
		sb.WriteString(n.ETag)
	}
	w.Header().Set("ETag", `"`+computeEtag(sb.String())+`"`)
	writeJSON(w, http.StatusOK, notes)
}

func (h *Handler) getNoteByID(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	meta, err := h.db.GetNoteMetaByID(id, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if meta == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	content, err := os.ReadFile(filepath.Join(h.notesDir(user.Username), meta.Filename))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	note := noteToResponse(meta, string(content))
	w.Header().Set("ETag", `"`+note.ETag+`"`)
	writeJSON(w, http.StatusOK, note)
}

func (h *Handler) createNote(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	var req noteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	title := "New note"
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = sanitizeTitle(*req.Title)
	}
	category := ""
	if req.Category != nil {
		category = *req.Category
	}
	content := ""
	if req.Content != nil {
		content = *req.Content
	}
	favorite := false
	if req.Favorite != nil {
		favorite = *req.Favorite
	}
	modifiedAt := time.Now().Unix()
	if req.Modified != nil && *req.Modified > 0 {
		modifiedAt = *req.Modified
	}

	notesDir := h.notesDir(user.Username)
	if err := os.MkdirAll(notesDir, 0755); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(filepath.Join(h.dataDir, user.Username, "attachments"), 0755); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	filename := uniqueFilename(notesDir, title)
	if err := os.WriteFile(filepath.Join(notesDir, filename), []byte(content), 0644); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	noteID, err := h.db.CreateNoteMeta(user.ID, filename, category, favorite, modifiedAt)
	if err != nil {
		os.Remove(filepath.Join(notesDir, filename)) //nolint:errcheck
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	meta := &db.NoteMeta{
		ID:         noteID,
		UserID:     user.ID,
		Filename:   filename,
		Category:   category,
		IsFavorite: favorite,
		ModifiedAt: modifiedAt,
	}
	writeJSON(w, http.StatusOK, noteToResponse(meta, content))
}

func (h *Handler) updateNote(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	meta, err := h.db.GetNoteMetaByID(id, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if meta == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	notesDir := h.notesDir(user.Username)
	currentBytes, err := os.ReadFile(filepath.Join(notesDir, meta.Filename))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	currentContent := string(currentBytes)

	// Optimistic concurrency control: If-Match must equal the current ETag.
	// Strip surrounding quotes the client may include per HTTP spec.
	if ifMatch := strings.Trim(r.Header.Get("If-Match"), `"`); ifMatch != "" {
		if ifMatch != computeEtag(currentContent) {
			current := noteToResponse(meta, currentContent)
			w.Header().Set("ETag", `"`+current.ETag+`"`)
			writeJSON(w, http.StatusPreconditionFailed, current)
			return
		}
	}

	var req noteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	newContent := currentContent
	if req.Content != nil {
		newContent = *req.Content
	}
	newCategory := meta.Category
	if req.Category != nil {
		newCategory = *req.Category
	}
	newFavorite := meta.IsFavorite
	if req.Favorite != nil {
		newFavorite = *req.Favorite
	}
	newModified := time.Now().Unix()
	if req.Modified != nil && *req.Modified > 0 {
		newModified = *req.Modified
	}

	// Resolve the new filename, treating the old file as available (it is
	// about to be freed if the title changes).
	newFilename := meta.Filename
	if req.Title != nil {
		newTitle := sanitizeTitle(*req.Title)
		newFilename = uniqueFilenameExcluding(notesDir, newTitle, meta.Filename)
	}

	if err := os.WriteFile(filepath.Join(notesDir, newFilename), []byte(newContent), 0644); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Remove the old file only if it was replaced by a new name.
	if newFilename != meta.Filename {
		os.Remove(filepath.Join(notesDir, meta.Filename)) //nolint:errcheck
	}

	if err := h.db.UpdateNoteFilename(id, user.ID, newFilename); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.db.UpdateNoteMeta(id, user.ID, newCategory, newFavorite, newModified); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	updatedMeta := &db.NoteMeta{
		ID:         id,
		UserID:     user.ID,
		Filename:   newFilename,
		Category:   newCategory,
		IsFavorite: newFavorite,
		ModifiedAt: newModified,
	}
	writeJSON(w, http.StatusOK, noteToResponse(updatedMeta, newContent))
}

func (h *Handler) deleteNote(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	meta, err := h.db.GetNoteMetaByID(id, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if meta == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	os.Remove(filepath.Join(h.notesDir(user.Username), meta.Filename)) //nolint:errcheck

	if err := h.db.DeleteNoteMeta(id, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- Settings handlers ---

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, appSettings{
		NotesPath:  "Notes",
		FileSuffix: ".md",
	})
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var s appSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil && err != io.EOF {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if s.FileSuffix != ".md" && s.FileSuffix != ".txt" {
		s.FileSuffix = ".md"
	}
	if s.NotesPath == "" {
		s.NotesPath = "Notes"
	}
	writeJSON(w, http.StatusOK, s)
}
