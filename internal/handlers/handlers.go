package handlers

import (
	"github.com/go-chi/chi/v5"

	"github.com/dermetti/quillbridge/internal/db"
)

// RegisterRoutes mounts all Notes API routes onto r.
func RegisterRoutes(r chi.Router, database *db.DB, dataDir string) {
	h := New(database, dataDir)

	r.Route("/apps/notes/api/v1", func(r chi.Router) {
		r.Route("/notes", func(r chi.Router) {
			r.Get("/", h.getNotes)
			r.Post("/", h.createNote)
			r.Get("/{id}", h.getNoteByID)
			r.Put("/{id}", h.updateNote)
			r.Delete("/{id}", h.deleteNote)
		})
		r.Get("/settings", h.getSettings)
		r.Put("/settings", h.putSettings)
	})

	r.Route("/apps/notes/api/v1.4/attachment/{id}", func(r chi.Router) {
		r.Post("/", h.uploadAttachment)
		r.Get("/", h.getAttachment)
	})
}

// RegisterUIRoutes mounts the Web UI routes.
// loginGroup: unauthenticated (GET/POST /login).
// authedGroup: session-authenticated (/, /logout, /user/*, /admin/*).
func RegisterUIRoutes(loginGroup chi.Router, authedGroup chi.Router, database *db.DB, dataDir string) {
	h := New(database, dataDir)

	loginGroup.Get("/login", h.getLogin)
	loginGroup.Post("/login", h.postLogin)

	authedGroup.Post("/logout", h.postLogout)
	authedGroup.Get("/", h.getDashboard)
	authedGroup.Post("/user/password", h.postUserPassword)
	authedGroup.Post("/user/files/upload", h.postUserFileUpload)
	authedGroup.Post("/user/files/delete", h.postUserFileDelete)

	authedGroup.Get("/admin", h.getAdmin)
	authedGroup.Post("/admin/users", h.postAdminCreateUser)
	authedGroup.Post("/admin/users/{id}/delete", h.postAdminDeleteUser)
	authedGroup.Post("/admin/users/{id}/quota", h.postAdminSetQuota)
	authedGroup.Post("/admin/users/{id}/password", h.postAdminSetPassword)
}
