package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type AgentDocument struct {
	ID         string
	AgentID    string
	Filename   string
	RawText    string
	UploadedAt time.Time
}

func (s *Store) migrateDocuments() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS agent_documents (
	id TEXT PRIMARY KEY,
	agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	filename TEXT NOT NULL,
	raw_text TEXT NOT NULL,
	uploaded_at TEXT NOT NULL
)`)
	if err != nil {
		return err
	}
	return s.migrateFixTimestamps()
}

func (s *Store) migrateFixTimestamps() error {
	queries := []string{
		`UPDATE users SET email_verified_at = replace(email_verified_at, ' ', 'T') || 'Z'
		 WHERE email_verified_at IS NOT NULL AND email_verified_at NOT LIKE '%T%'`,
		`UPDATE agent_access_tokens SET expires_at = replace(expires_at, ' ', 'T') || 'Z'
		 WHERE expires_at NOT LIKE '%T%'`,
		`UPDATE agent_documents SET uploaded_at = replace(uploaded_at, ' ', 'T') || 'Z'
		 WHERE uploaded_at NOT LIKE '%T%'`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateAgentDocument(ctx context.Context, id, agentID, filename, rawText string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_documents (id, agent_id, filename, raw_text, uploaded_at) VALUES (?, ?, ?, ?, ?)`,
		id, agentID, filename, rawText, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) ListAgentDocuments(ctx context.Context, agentID string) ([]AgentDocument, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, filename, raw_text, uploaded_at FROM agent_documents WHERE agent_id = ? ORDER BY uploaded_at ASC`,
		agentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []AgentDocument
	for rows.Next() {
		var d AgentDocument
		var uploadedRaw string
		if err := rows.Scan(&d.ID, &d.AgentID, &d.Filename, &d.RawText, &uploadedRaw); err != nil {
			return nil, err
		}
		d.UploadedAt, err = parseStoredTime( uploadedRaw)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (s *Store) HasAgentDocuments(ctx context.Context, agentID string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_documents WHERE agent_id = ?`, agentID)
	var n int
	if err := row.Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) DocumentTextsForAgent(ctx context.Context, agentID string) ([]string, error) {
	docs, err := s.ListAgentDocuments(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		if strings.TrimSpace(d.RawText) != "" {
			out = append(out, d.RawText)
		}
	}
	return out, nil
}

func (s *Store) DeleteAgentDocument(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_documents WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AgentDocumentByID(ctx context.Context, id string) (AgentDocument, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, filename, raw_text, uploaded_at FROM agent_documents WHERE id = ?`, id,
	)
	var d AgentDocument
	var uploadedRaw string
	err := row.Scan(&d.ID, &d.AgentID, &d.Filename, &d.RawText, &uploadedRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentDocument{}, ErrNotFound
		}
		return AgentDocument{}, err
	}
	d.UploadedAt, err = parseStoredTime( uploadedRaw)
	if err != nil {
		return AgentDocument{}, err
	}
	return d, nil
}
