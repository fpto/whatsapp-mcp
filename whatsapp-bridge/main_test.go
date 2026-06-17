package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newTestStore creates a MessageStore backed by a throwaway SQLite file with the
// schema needed by the LID-mapping tests.
func newTestStore(t *testing.T) *MessageStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			PRIMARY KEY (id, chat_jid)
		);
		CREATE TABLE lid_mapping (
			lid TEXT PRIMARY KEY,
			phone TEXT NOT NULL,
			updated_at TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	store := &MessageStore{db: db}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestStoreLIDMappingAndLookup(t *testing.T) {
	store := newTestStore(t)

	if err := store.StoreLIDMapping("111", "5511999"); err != nil {
		t.Fatalf("StoreLIDMapping failed: %v", err)
	}

	phone, ok := store.GetPhoneForLID("111")
	if !ok || phone != "5511999" {
		t.Fatalf("GetPhoneForLID = (%q, %v), want (\"5511999\", true)", phone, ok)
	}

	if _, ok := store.GetPhoneForLID("999"); ok {
		t.Fatalf("GetPhoneForLID for unknown LID should return false")
	}
}

func TestStoreLIDMappingIgnoresInvalidInput(t *testing.T) {
	store := newTestStore(t)

	cases := [][2]string{{"", "5511999"}, {"111", ""}, {"111", "111"}}
	for _, c := range cases {
		if err := store.StoreLIDMapping(c[0], c[1]); err != nil {
			t.Fatalf("StoreLIDMapping(%q,%q) returned error: %v", c[0], c[1], err)
		}
	}

	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM lid_mapping").Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no mappings to be stored, got %d", count)
	}
}

func TestStoreLIDMappingBackfillsMessages(t *testing.T) {
	store := newTestStore(t)

	// A message stored before the phone number was known, attributed to the LID.
	if _, err := store.db.Exec(
		"INSERT INTO messages (id, chat_jid, sender, content) VALUES ('msg1', 'group@g.us', '111', 'hello')",
	); err != nil {
		t.Fatalf("failed to seed message: %v", err)
	}

	if err := store.StoreLIDMapping("111", "5511999"); err != nil {
		t.Fatalf("StoreLIDMapping failed: %v", err)
	}

	var sender string
	if err := store.db.QueryRow("SELECT sender FROM messages WHERE id = 'msg1'").Scan(&sender); err != nil {
		t.Fatalf("sender query failed: %v", err)
	}
	if sender != "5511999" {
		t.Fatalf("expected message sender backfilled to phone number, got %q", sender)
	}
}

func TestStoreLIDMappingIsIdempotent(t *testing.T) {
	store := newTestStore(t)

	if err := store.StoreLIDMapping("111", "5511999"); err != nil {
		t.Fatalf("first StoreLIDMapping failed: %v", err)
	}
	// Re-learning the same pair must not create duplicates or error.
	if err := store.StoreLIDMapping("111", "5511999"); err != nil {
		t.Fatalf("second StoreLIDMapping failed: %v", err)
	}
	// Learning a corrected phone number updates the existing row.
	if err := store.StoreLIDMapping("111", "5511888"); err != nil {
		t.Fatalf("third StoreLIDMapping failed: %v", err)
	}

	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM lid_mapping WHERE lid = '111'").Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one row for LID, got %d", count)
	}

	phone, _ := store.GetPhoneForLID("111")
	if phone != "5511888" {
		t.Fatalf("expected updated phone 5511888, got %q", phone)
	}
}
