package db

import (
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- User tests ---

func TestCreateAndGetUser(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateUser("alice", "hash1", 104857600, false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	u, err := db.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if u == nil {
		t.Fatal("expected user, got nil")
	}
	if u.Username != "alice" || u.PasswordHash != "hash1" || u.QuotaBytes != 104857600 || u.IsAdmin {
		t.Errorf("unexpected user fields: %+v", u)
	}
}

func TestGetUserByUsername_NotFound(t *testing.T) {
	db := openTestDB(t)
	u, err := db.GetUserByUsername("nobody")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Fatalf("expected nil, got %+v", u)
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateUser("bob", "hash", 100, false); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	if err := db.CreateUser("bob", "hash2", 100, false); err == nil {
		t.Fatal("expected error for duplicate username, got nil")
	}
}

func TestGetAllUsers(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("u1", "h1", 100, false)
	db.CreateUser("u2", "h2", 200, true)

	users, err := db.GetAllUsers()
	if err != nil {
		t.Fatalf("GetAllUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestUpdateUserPassword(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("carol", "oldhash", 100, false)
	u, _ := db.GetUserByUsername("carol")

	if err := db.UpdateUserPassword(u.ID, "newhash"); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	updated, _ := db.GetUserByUsername("carol")
	if updated.PasswordHash != "newhash" {
		t.Errorf("expected newhash, got %s", updated.PasswordHash)
	}
}

func TestUpdateUserQuota(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("dave", "hash", 100, false)
	u, _ := db.GetUserByUsername("dave")

	if err := db.UpdateUserQuota(u.ID, 999999); err != nil {
		t.Fatalf("UpdateUserQuota: %v", err)
	}
	updated, _ := db.GetUserByUsername("dave")
	if updated.QuotaBytes != 999999 {
		t.Errorf("expected 999999, got %d", updated.QuotaBytes)
	}
}

func TestDeleteUser(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("eve", "hash", 100, false)
	u, _ := db.GetUserByUsername("eve")

	if err := db.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	gone, _ := db.GetUserByUsername("eve")
	if gone != nil {
		t.Fatal("expected user to be deleted")
	}
}

// --- Session tests ---

func TestCreateAndGetSession(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("frank", "hash", 100, false)
	u, _ := db.GetUserByUsername("frank")

	exp := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	if err := db.CreateSession("tok123", u.ID, exp); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	s, err := db.GetSession("tok123")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s == nil {
		t.Fatal("expected session, got nil")
	}
	if s.UserID != u.ID {
		t.Errorf("expected userID %d, got %d", u.ID, s.UserID)
	}
	if !s.ExpiresAt.Equal(exp) {
		t.Errorf("expected %v, got %v", exp, s.ExpiresAt)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	db := openTestDB(t)
	s, err := db.GetSession("notoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != nil {
		t.Fatalf("expected nil, got %+v", s)
	}
}

func TestDeleteSession(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("grace", "hash", 100, false)
	u, _ := db.GetUserByUsername("grace")
	db.CreateSession("tok456", u.ID, time.Now().Add(time.Hour))

	if err := db.DeleteSession("tok456"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	gone, _ := db.GetSession("tok456")
	if gone != nil {
		t.Fatal("expected session to be deleted")
	}
}

// --- NoteMeta tests ---

func TestCreateAndGetNoteMeta(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("henry", "hash", 100, false)
	u, _ := db.GetUserByUsername("henry")

	id, err := db.CreateNoteMeta(u.ID, "note1.md", "work", false, 1700000000)
	if err != nil {
		t.Fatalf("CreateNoteMeta: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	n, err := db.GetNoteMetaByID(id, u.ID)
	if err != nil {
		t.Fatalf("GetNoteMetaByID: %v", err)
	}
	if n == nil {
		t.Fatal("expected note, got nil")
	}
	if n.Filename != "note1.md" || n.Category != "work" || n.IsFavorite || n.ModifiedAt != 1700000000 {
		t.Errorf("unexpected note fields: %+v", n)
	}
}

func TestGetNoteMetaByID_NotFound(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("ida", "hash", 100, false)
	u, _ := db.GetUserByUsername("ida")

	n, err := db.GetNoteMetaByID(999, u.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != nil {
		t.Fatalf("expected nil, got %+v", n)
	}
}

func TestGetNoteMetaByID_WrongUser(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("user1", "hash", 100, false)
	db.CreateUser("user2", "hash", 100, false)
	u1, _ := db.GetUserByUsername("user1")
	u2, _ := db.GetUserByUsername("user2")

	id, _ := db.CreateNoteMeta(u1.ID, "secret.md", "", false, 1700000000)

	n, err := db.GetNoteMetaByID(id, u2.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != nil {
		t.Fatal("should not return another user's note")
	}
}

func TestGetAllNotesMeta(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("jack", "hash", 100, false)
	u, _ := db.GetUserByUsername("jack")

	db.CreateNoteMeta(u.ID, "a.md", "", false, 1)
	db.CreateNoteMeta(u.ID, "b.md", "cat", true, 2)

	notes, err := db.GetAllNotesMeta(u.ID)
	if err != nil {
		t.Fatalf("GetAllNotesMeta: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
}

func TestGetAllNotesMeta_Empty(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("kate", "hash", 100, false)
	u, _ := db.GetUserByUsername("kate")

	notes, err := db.GetAllNotesMeta(u.ID)
	if err != nil {
		t.Fatalf("GetAllNotesMeta: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("expected 0 notes, got %d", len(notes))
	}
}

func TestUpdateNoteMeta(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("liam", "hash", 100, false)
	u, _ := db.GetUserByUsername("liam")

	id, _ := db.CreateNoteMeta(u.ID, "note.md", "", false, 100)

	if err := db.UpdateNoteMeta(id, u.ID, "personal", true, 200); err != nil {
		t.Fatalf("UpdateNoteMeta: %v", err)
	}
	n, _ := db.GetNoteMetaByID(id, u.ID)
	if n.Category != "personal" || !n.IsFavorite || n.ModifiedAt != 200 {
		t.Errorf("unexpected note after update: %+v", n)
	}
}

func TestDeleteNoteMeta(t *testing.T) {
	db := openTestDB(t)
	db.CreateUser("mia", "hash", 100, false)
	u, _ := db.GetUserByUsername("mia")

	id, _ := db.CreateNoteMeta(u.ID, "gone.md", "", false, 1)

	if err := db.DeleteNoteMeta(id, u.ID); err != nil {
		t.Fatalf("DeleteNoteMeta: %v", err)
	}
	gone, _ := db.GetNoteMetaByID(id, u.ID)
	if gone != nil {
		t.Fatal("expected note to be deleted")
	}
}
