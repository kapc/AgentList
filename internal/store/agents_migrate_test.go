package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateAgentsMultiType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate legacy schema: one agent per user_id.
	if _, err := db.db.Exec(`
CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
INSERT INTO users (id, email, password_hash, created_at) VALUES ('u1', 'a@test.com', 'x', '2026-01-01T00:00:00Z');
DROP TABLE IF EXISTS agents;
CREATE TABLE agents (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL UNIQUE,
	slug TEXT NOT NULL UNIQUE,
	published INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT 'jobseeker'
);
INSERT INTO agents (id, user_id, type, slug, published, created_at)
VALUES ('a1', 'u1', 'jobseeker', 'js-u1', 0, '2026-01-01T00:00:00Z');
`); err != nil {
		t.Fatal(err)
	}

	if err := db.migrateAgentsMultiType(); err != nil {
		t.Fatal(err)
	}
	if db.agentsHasLegacyUserUnique() {
		t.Fatal("expected legacy unique constraint to be removed")
	}

	if err := db.CreateAgent(context.Background(), "a2", "u1", "doctor", "dr-u1"); err != nil {
		t.Fatalf("create second agent type: %v", err)
	}

	_ = db.Close()
	_ = os.Remove(path)
}
