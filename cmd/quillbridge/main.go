package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"github.com/dermetti/quillbridge/internal/db"
	"github.com/dermetti/quillbridge/internal/handlers"
	mw "github.com/dermetti/quillbridge/internal/middleware"
)

const defaultAdminUser = "quillbridgeadmin"
const defaultAdminPass = "quillbridgepass"
const dataDir = "./data"

func main() {
	// Ensure ./data directory exists.
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		slog.Error("failed to create data directory", "err", err)
		os.Exit(1)
	}

	// Open (or create) the SQLite database.
	database, err := db.Open(filepath.Join(dataDir, "quillbridge.db"))
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	// Seed admin user if no users exist.
	if err := seedAdmin(database); err != nil {
		slog.Error("failed to seed admin user", "err", err)
		os.Exit(1)
	}

	r := chi.NewRouter()

	// Global middleware.
	r.Use(chimiddleware.Logger)
	r.Use(mw.PathScrubber)

	// OCS capabilities endpoint — required by Quillpad on first connect.
	r.Get("/ocs/v2.php/cloud/capabilities", capabilitiesHandler)

	// Notes API routes: require Basic Auth, quota enforcement, and the version header.
	r.Group(func(r chi.Router) {
		r.Use(mw.NotesAPIVersionHeader)
		r.Use(mw.BasicAuth(database))
		r.Use(mw.QuotaCheck(dataDir))
		handlers.RegisterRoutes(r, database, dataDir)
	})

	// Web UI: /login is unauthenticated; everything else requires a session.
	r.Group(func(authed chi.Router) {
		authed.Use(mw.SessionAuth(database))
		handlers.RegisterUIRoutes(r, authed, database, dataDir)
	})

	slog.Info("listening on :8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

// capabilitiesHandler returns OCS capabilities indicating Notes API v1.4 support.
func capabilitiesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Notes-API-Versions", mw.NotesAPIVersions)

	resp := map[string]any{
		"ocs": map[string]any{
			"data": map[string]any{
				"capabilities": map[string]any{
					"notes": map[string]any{
						"api_version": []string{"1.0", "1.1", "1.2", "1.3", "1.4"},
						"version":     "1.0.0",
					},
				},
			},
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func seedAdmin(database *db.DB) error {
	users, err := database.GetAllUsers()
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return nil
	}

	username := os.Getenv("ADMIN_USER")
	if username == "" {
		username = defaultAdminUser
	}
	password := os.Getenv("ADMIN_PASS")
	if password == "" {
		password = defaultAdminPass
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if err := database.CreateUser(username, string(hash), 104857600, true); err != nil {
		return err
	}

	// Create storage directories for the admin user.
	for _, sub := range []string{"notes", "attachments"} {
		dir := filepath.Join(dataDir, username, sub)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	slog.Info("created admin user", "username", username)
	return nil
}
