package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) UpsertGoogleCalendarConnection(ctx context.Context, conn GoogleCalendarConnection) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO google_calendar_connections
		 (agent_id, user_id, calendar_id, access_token, refresh_token, token_expiry, connected_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   calendar_id = excluded.calendar_id,
		   access_token = excluded.access_token,
		   refresh_token = excluded.refresh_token,
		   token_expiry = excluded.token_expiry,
		   connected_at = excluded.connected_at`,
		conn.AgentID, conn.UserID, conn.CalendarID,
		conn.AccessToken, conn.RefreshToken,
		conn.TokenExpiry.UTC().Format(time.RFC3339),
		conn.ConnectedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) GoogleCalendarConnectionByAgentID(ctx context.Context, agentID string) (GoogleCalendarConnection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT agent_id, user_id, calendar_id, access_token, refresh_token, token_expiry, connected_at
		 FROM google_calendar_connections WHERE agent_id = ?`,
		agentID,
	)
	return scanGoogleCalendarConnection(row)
}

func (s *Store) DeleteGoogleCalendarConnection(ctx context.Context, agentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM google_calendar_connections WHERE agent_id = ?`, agentID)
	return err
}

func (s *Store) CreateAppointment(ctx context.Context, appt Appointment) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appointments
		 (id, agent_id, session_id, google_event_id, starts_at, ends_at, patient_name, patient_email, patient_phone, service_type, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		appt.ID, appt.AgentID, nullIfEmpty(appt.SessionID), appt.GoogleEventID,
		appt.StartsAt.UTC().Format(time.RFC3339),
		appt.EndsAt.UTC().Format(time.RFC3339),
		appt.PatientName, appt.PatientEmail, appt.PatientPhone,
		appt.ServiceType, appt.Status,
		appt.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func scanGoogleCalendarConnection(row interface {
	Scan(dest ...any) error
}) (GoogleCalendarConnection, error) {
	var conn GoogleCalendarConnection
	var expiryRaw, connectedRaw string
	err := row.Scan(
		&conn.AgentID, &conn.UserID, &conn.CalendarID,
		&conn.AccessToken, &conn.RefreshToken, &expiryRaw, &connectedRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GoogleCalendarConnection{}, ErrNotFound
		}
		return GoogleCalendarConnection{}, err
	}
	conn.TokenExpiry, err = parseStoredTime(expiryRaw)
	if err != nil {
		return GoogleCalendarConnection{}, err
	}
	conn.ConnectedAt, err = parseStoredTime(connectedRaw)
	if err != nil {
		return GoogleCalendarConnection{}, err
	}
	return conn, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
