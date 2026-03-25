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

	// logo.svg
	r.Get("/logo.svg", logoHandler)

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
			"meta": map[string]any{
				"status":     "ok",
				"statuscode": 200,
				"message":    "OK",
			},
			"data": map[string]any{
				"capabilities": map[string]any{
					"notes": map[string]any{
						"api_version": []string{"1.4"},
						"version":     "4.9.0",
						"attachments": true,
					},
				},
			},
		},
	}
	json.NewEncoder(w).Encode(resp)
}

// logo handler function
func logoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
        <rect width="100" height="100" rx="20" fill="#007bff"/>
        <path d="M30 70 L50 30 L70 70" stroke="white" stroke-width="8" fill="none"/>
    </svg>`
	w.Write([]byte(svg))
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
