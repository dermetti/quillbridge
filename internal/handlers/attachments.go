package handlers

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"

	mw "github.com/dermetti/quillbridge/internal/middleware"
)

// maxUploadBytes is the hard cap on a single multipart upload.
const maxUploadBytes = 64 << 20 // 64 MB

// uploadAttachment handles POST /apps/notes/api/v1.4/attachment/{id}.
// It accepts a multipart/form-data request with a "file" field, stores the
// file in the user's attachments directory, and returns {"filename":"..."}.
// The filename is MD5(content)+original_extension, matching Nextcloud's scheme.
func (h *Handler) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	noteID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}
	meta, err := h.db.GetNoteMetaByID(noteID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if meta == nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "file too large or malformed form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Generate a content-addressed filename: MD5(bytes) + original extension.
	sum := md5.Sum(data)
	filename := hex.EncodeToString(sum[:]) + filepath.Ext(header.Filename)

	attachDir := filepath.Join(h.dataDir, user.Username, "attachments")
	if err := os.MkdirAll(attachDir, 0755); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(attachDir, filename), data, 0644); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"filename": filename}) //nolint:errcheck
}

// getAttachment handles GET /apps/notes/api/v1.4/attachment/{id}?path={filename}.
// It serves the requested file from the user's attachments directory.
// filepath.Base is applied to the path parameter to prevent directory traversal.
func (h *Handler) getAttachment(w http.ResponseWriter, r *http.Request) {
	user := mw.UserFromContext(r.Context())

	noteID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}
	meta, err := h.db.GetNoteMetaByID(noteID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if meta == nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}

	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		http.Error(w, "missing 'path' query parameter", http.StatusBadRequest)
		return
	}

	// Strip any directory component to prevent traversal attacks.
	filename := filepath.Base(rawPath)
	if filename == "." || filename == ".." || filename == "" {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	attachPath := filepath.Join(h.dataDir, user.Username, "attachments", filename)

	// Return 404 explicitly if the file does not exist, rather than letting
	// http.ServeFile emit a directory listing or redirect.
	if _, err := os.Stat(attachPath); os.IsNotExist(err) {
		http.Error(w, "attachment not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, attachPath)
}
