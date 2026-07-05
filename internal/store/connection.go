package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	ConnectionStatusPending  = "pending"
	ConnectionStatusApproved = "approved"
	ConnectionStatusDeclined = "declined"
	ConnectionStatusExpired  = "expired"
)

var ErrConnectionExists = errors.New("connection request already exists for session")

type ConnectionRequest struct {
	ID               string
	SessionID        string
	AgentID          string
	Status           string
	RecruiterName    string
	RecruiterCompany string
	RecruiterEmail   string
	RecruiterNote    string
	ScreeningSummary string
	CreatedAt        time.Time
	DecidedAt        *time.Time
	ExpiresAt        time.Time
}

func (s *Store) CreateConnectionRequest(ctx context.Context, req ConnectionRequest) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO connection_requests (
	id, session_id, agent_id, status,
	recruiter_name, recruiter_company, recruiter_email, recruiter_note,
	screening_summary, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.SessionID, req.AgentID, req.Status,
		req.RecruiterName, req.RecruiterCompany, req.RecruiterEmail, req.RecruiterNote,
		req.ScreeningSummary, req.CreatedAt.UTC().Format(time.RFC3339), req.ExpiresAt.UTC().Format(time.RFC3339),
	)
	if err != nil && isUniqueViolation(err) {
		return ErrConnectionExists
	}
	return err
}

func (s *Store) ConnectionRequestByID(ctx context.Context, id string) (ConnectionRequest, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, session_id, agent_id, status,
	recruiter_name, recruiter_company, recruiter_email, recruiter_note,
	screening_summary, created_at, decided_at, expires_at
FROM connection_requests WHERE id = ?`, id)
	return scanConnectionRequest(row)
}

func (s *Store) ConnectionRequestBySessionID(ctx context.Context, sessionID string) (ConnectionRequest, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, session_id, agent_id, status,
	recruiter_name, recruiter_company, recruiter_email, recruiter_note,
	screening_summary, created_at, decided_at, expires_at
FROM connection_requests WHERE session_id = ?`, sessionID)
	return scanConnectionRequest(row)
}

func (s *Store) ListConnectionRequestsForUser(ctx context.Context, userID string, statuses ...string) ([]ConnectionRequest, error) {
	query := `
SELECT cr.id, cr.session_id, cr.agent_id, cr.status,
	cr.recruiter_name, cr.recruiter_company, cr.recruiter_email, cr.recruiter_note,
	cr.screening_summary, cr.created_at, cr.decided_at, cr.expires_at
FROM connection_requests cr
JOIN agents a ON a.id = cr.agent_id
WHERE a.user_id = ?`
	args := []any{userID}

	if len(statuses) > 0 {
		query += ` AND cr.status IN (` + placeholders(len(statuses)) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	query += ` ORDER BY cr.created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConnectionRequest
	for rows.Next() {
		req, err := scanConnectionRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func (s *Store) UpdateConnectionRequestStatus(ctx context.Context, id, status string, decidedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE connection_requests SET status = ?, decided_at = ?
WHERE id = ? AND status = ?`,
		status, decidedAt.UTC().Format(time.RFC3339), id, ConnectionStatusPending,
	)
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

func (s *Store) ExpireConnectionRequestIfNeeded(ctx context.Context, req *ConnectionRequest) {
	if req.Status != ConnectionStatusPending {
		return
	}
	if time.Now().UTC().After(req.ExpiresAt) {
		now := time.Now().UTC()
		_, _ = s.db.ExecContext(ctx, `
UPDATE connection_requests SET status = ?, decided_at = ?
WHERE id = ? AND status = ?`,
			ConnectionStatusExpired, now.Format(time.RFC3339), req.ID, ConnectionStatusPending,
		)
		req.Status = ConnectionStatusExpired
		req.DecidedAt = &now
	}
}

func scanConnectionRequest(row *sql.Row) (ConnectionRequest, error) {
	var req ConnectionRequest
	var createdRaw, expiresRaw string
	var decidedRaw sql.NullString
	err := row.Scan(
		&req.ID, &req.SessionID, &req.AgentID, &req.Status,
		&req.RecruiterName, &req.RecruiterCompany, &req.RecruiterEmail, &req.RecruiterNote,
		&req.ScreeningSummary, &createdRaw, &decidedRaw, &expiresRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ConnectionRequest{}, ErrNotFound
		}
		return ConnectionRequest{}, err
	}
	req.CreatedAt, err = parseStoredTime( createdRaw)
	if err != nil {
		return ConnectionRequest{}, err
	}
	req.ExpiresAt, err = parseStoredTime( expiresRaw)
	if err != nil {
		return ConnectionRequest{}, err
	}
	if decidedRaw.Valid {
		t, err := parseStoredTime( decidedRaw.String)
		if err != nil {
			return ConnectionRequest{}, err
		}
		req.DecidedAt = &t
	}
	return req, nil
}

func scanConnectionRequestRow(rows *sql.Rows) (ConnectionRequest, error) {
	var req ConnectionRequest
	var createdRaw, expiresRaw string
	var decidedRaw sql.NullString
	err := rows.Scan(
		&req.ID, &req.SessionID, &req.AgentID, &req.Status,
		&req.RecruiterName, &req.RecruiterCompany, &req.RecruiterEmail, &req.RecruiterNote,
		&req.ScreeningSummary, &createdRaw, &decidedRaw, &expiresRaw,
	)
	if err != nil {
		return ConnectionRequest{}, err
	}
	req.CreatedAt, err = parseStoredTime( createdRaw)
	if err != nil {
		return ConnectionRequest{}, err
	}
	req.ExpiresAt, err = parseStoredTime( expiresRaw)
	if err != nil {
		return ConnectionRequest{}, err
	}
	if decidedRaw.Valid {
		t, err := parseStoredTime( decidedRaw.String)
		if err != nil {
			return ConnectionRequest{}, err
		}
		req.DecidedAt = &t
	}
	return req, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := "?"
	for i := 1; i < n; i++ {
		s += ",?"
	}
	return s
}
