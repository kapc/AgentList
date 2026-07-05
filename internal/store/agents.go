package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/chandresh/agentlist/internal/agenttypes"
)

var ErrProfileRequired = errors.New("agent profile required")

func scanAgentRow(row *sql.Row) (Agent, error) {
	var a Agent
	var published int
	var createdRaw string
	err := row.Scan(&a.ID, &a.UserID, &a.Type, &a.Slug, &published, &createdRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, err
	}
	a.Published = published == 1
	a.CreatedAt, err = parseStoredTime( createdRaw)
	if err != nil {
		return Agent{}, err
	}
	return a, nil
}

func (s *Store) CreateAgent(ctx context.Context, id, userID, agentType, slug string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, user_id, type, slug, published, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		id, userID, agentType, slug, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil && isUniqueViolation(err) {
		return ErrAgentExists
	}
	return err
}

func (s *Store) AgentByUserAndType(ctx context.Context, userID, agentType string) (Agent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, type, slug, published, created_at FROM agents WHERE user_id = ? AND type = ?`,
		userID, agentType,
	)
	return scanAgentRow(row)
}

func (s *Store) ListAgentsByUser(ctx context.Context, userID string) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, type, slug, published, created_at FROM agents WHERE user_id = ? ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		var published int
		var createdRaw string
		if err := rows.Scan(&a.ID, &a.UserID, &a.Type, &a.Slug, &published, &createdRaw); err != nil {
			return nil, err
		}
		a.Published = published == 1
		a.CreatedAt, err = parseStoredTime( createdRaw)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *Store) PublishAgentByID(ctx context.Context, agentID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET published = 1 WHERE id = ?`, agentID)
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

func (s *Store) UpsertAgentProfile(ctx context.Context, agentID, profileJSON, contextText string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_profiles (agent_id, profile_json, context_text, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(agent_id) DO UPDATE SET
	profile_json = excluded.profile_json,
	context_text = excluded.context_text,
	updated_at = excluded.updated_at`,
		agentID, profileJSON, contextText, now,
	)
	return err
}

func (s *Store) AgentProfileByAgentID(ctx context.Context, agentID string) (AgentProfile, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT agent_id, profile_json, context_text, updated_at FROM agent_profiles WHERE agent_id = ?`, agentID,
	)
	var p AgentProfile
	var updatedRaw string
	err := row.Scan(&p.AgentID, &p.ProfileJSON, &p.ContextText, &updatedRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentProfile{}, ErrNotFound
		}
		return AgentProfile{}, err
	}
	p.UpdatedAt, err = parseStoredTime( updatedRaw)
	if err != nil {
		return AgentProfile{}, err
	}
	return p, nil
}

func (s *Store) ContextForAgent(ctx context.Context, agent Agent) (string, error) {
	docTexts, err := s.DocumentTextsForAgent(ctx, agent.ID)
	if err != nil {
		return "", err
	}
	hasDocs := len(docTexts) > 0

	if agent.Type == agenttypes.Jobseeker || agent.Type == "" {
		exp := ""
		if rec, err := s.ExperienceContextForAgent(ctx, agent.ID); err == nil {
			exp = rec
		}
		profile := map[string]string{"context": exp}
		full := agenttypes.BuildContext(agenttypes.Jobseeker, profile, docTexts)
		if strings.TrimSpace(full) == "" || (exp == "" && !hasDocs) {
			return "", ErrResumeRequired
		}
		return full, nil
	}

	profile := map[string]string{}
	if prof, err := s.AgentProfileByAgentID(ctx, agent.ID); err == nil {
		_ = json.Unmarshal([]byte(prof.ProfileJSON), &profile)
	}
	full := agenttypes.BuildContext(agent.Type, profile, docTexts)
	if !agenttypes.ProfileComplete(agent.Type, profile, false, hasDocs) {
		return "", ErrProfileRequired
	}
	return full, nil
}
