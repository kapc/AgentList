package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type VoiceCallSession struct {
	CallSID       string
	AgentID       string
	ChatSessionID string
	CreatedAt     time.Time
}

func (s *Store) CreateVoiceCallSession(ctx context.Context, callSID, agentID string) (string, error) {
	sessionID := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO voice_call_sessions (call_sid, agent_id, chat_session_id, created_at) VALUES (?, ?, ?, ?)`,
		callSID, agentID, sessionID, now,
	)
	if err != nil {
		return "", err
	}
	if err := s.CreateChatSession(ctx, sessionID, agentID); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *Store) VoiceCallSessionByCallSID(ctx context.Context, callSID string) (VoiceCallSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT call_sid, agent_id, chat_session_id, created_at FROM voice_call_sessions WHERE call_sid = ?`,
		callSID,
	)
	var v VoiceCallSession
	var createdRaw string
	err := row.Scan(&v.CallSID, &v.AgentID, &v.ChatSessionID, &createdRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VoiceCallSession{}, ErrNotFound
		}
		return VoiceCallSession{}, err
	}
	v.CreatedAt, err = parseStoredTime(createdRaw)
	if err != nil {
		return VoiceCallSession{}, err
	}
	return v, nil
}
