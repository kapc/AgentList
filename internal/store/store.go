package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/chandresh/agentlist/internal/agenttypes"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrEmailTaken      = errors.New("email already registered")
	ErrInvalidCreds    = errors.New("invalid email or password")
	ErrEmailUnverified = errors.New("email not verified")
	ErrResumeRequired  = errors.New("resume required")
	ErrAgentUnpublished = errors.New("agent not published")
	ErrAgentLinkExpired = errors.New("agent link expired")
	ErrSessionLimit     = errors.New("session message limit reached")
	ErrSessionTokenLimit = errors.New("session token limit reached")
	ErrAgentExists       = errors.New("agent already exists")
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	email TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL DEFAULT '',
	email_verified_at TEXT,
	google_id TEXT UNIQUE,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS email_verification_tokens (
	token TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agents (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	type TEXT NOT NULL DEFAULT 'jobseeker',
	slug TEXT NOT NULL UNIQUE,
	published INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	UNIQUE(user_id, type)
);

CREATE TABLE IF NOT EXISTS agent_profiles (
	agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
	profile_json TEXT NOT NULL DEFAULT '{}',
	context_text TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS resumes (
	user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	filename TEXT NOT NULL,
	raw_text TEXT NOT NULL,
	identity_vault TEXT NOT NULL DEFAULT '{}',
	experience_context TEXT NOT NULL,
	uploaded_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_access_tokens (
	token TEXT PRIMARY KEY,
	agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_sessions (
	id TEXT PRIMARY KEY,
	agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_messages (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
	role TEXT NOT NULL,
	content TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS connection_requests (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL UNIQUE REFERENCES chat_sessions(id) ON DELETE CASCADE,
	agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	status TEXT NOT NULL,
	recruiter_name TEXT NOT NULL DEFAULT '',
	recruiter_company TEXT NOT NULL DEFAULT '',
	recruiter_email TEXT NOT NULL DEFAULT '',
	recruiter_note TEXT NOT NULL DEFAULT '',
	screening_summary TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	decided_at TEXT,
	expires_at TEXT NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	return s.migrateLegacy()
}

func (s *Store) migrateLegacy() error {
	// Upgrades for databases created before google_id existed.
	// SQLite cannot ADD COLUMN with an inline UNIQUE constraint, so add the
	// column plain and enforce uniqueness with a separate index. NULLs are
	// treated as distinct in a SQLite unique index, so password-only users
	// (google_id IS NULL) don't collide.
	if !s.columnExists("users", "google_id") {
		if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN google_id TEXT`); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_google_id ON users(google_id)`)
	if err != nil {
		return err
	}
	if !s.columnExists("chat_sessions", "tokens_used") {
		if _, err := s.db.Exec(`ALTER TABLE chat_sessions ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !s.columnExists("agents", "type") {
		if _, err := s.db.Exec(`ALTER TABLE agents ADD COLUMN type TEXT NOT NULL DEFAULT 'jobseeker'`); err != nil {
			return err
		}
	}
	_, _ = s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_user_type ON agents(user_id, type)`)
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_profiles (
		agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
		profile_json TEXT NOT NULL DEFAULT '{}',
		context_text TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL
	)`)
	if err := s.migrateAgentsMultiType(); err != nil {
		return err
	}
	return s.migrateDocuments()
}

// migrateAgentsMultiType rebuilds the agents table when legacy schema still has
// UNIQUE(user_id), which only allowed one agent per user (blocking doctor/gardener/etc.).
func (s *Store) migrateAgentsMultiType() error {
	if !s.agentsHasLegacyUserUnique() {
		return nil
	}
	_, err := s.db.Exec(`
PRAGMA foreign_keys=OFF;
BEGIN;
CREATE TABLE agents_new (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	type TEXT NOT NULL DEFAULT 'jobseeker',
	slug TEXT NOT NULL UNIQUE,
	published INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	UNIQUE(user_id, type)
);
INSERT INTO agents_new (id, user_id, type, slug, published, created_at)
SELECT id, user_id, COALESCE(NULLIF(type, ''), 'jobseeker'), slug, published, created_at FROM agents;
DROP TABLE agents;
ALTER TABLE agents_new RENAME TO agents;
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_user_type ON agents(user_id, type);
COMMIT;
PRAGMA foreign_keys=ON;
`)
	return err
}

func (s *Store) agentsHasLegacyUserUnique() bool {
	row := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'agents'`)
	var ddl string
	if err := row.Scan(&ddl); err != nil {
		return false
	}
	upper := strings.ToUpper(ddl)
	return strings.Contains(upper, "USER_ID TEXT NOT NULL UNIQUE")
}

func (s *Store) columnExists(table, column string) bool {
	rows, err := s.db.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false
	}
	defer rows.Close()
	return rows.Next()
}

func (s *Store) CreateUser(ctx context.Context, id, email, passwordHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		id, email, passwordHash, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		return err
	}
	return nil
}

func (s *Store) CreateVerificationToken(ctx context.Context, token, userID string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO email_verification_tokens (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, google_id, email_verified_at, created_at FROM users WHERE email = ? COLLATE NOCASE`,
		email,
	)
	return scanUser(row)
}

func (s *Store) UserByGoogleID(ctx context.Context, googleID string) (User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, google_id, email_verified_at, created_at FROM users WHERE google_id = ?`,
		googleID,
	)
	return scanUser(row)
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, google_id, email_verified_at, created_at FROM users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

func (s *Store) CreateGoogleUser(ctx context.Context, id, email, googleID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, google_id, email_verified_at, created_at) VALUES (?, ?, '', ?, ?, ?)`,
		id, email, googleID, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		return err
	}
	return nil
}

func (s *Store) LinkGoogleAccount(ctx context.Context, userID, googleID string, markVerified bool) error {
	if markVerified {
		now := time.Now().UTC().Format(time.RFC3339)
		_, err := s.db.ExecContext(ctx,
			`UPDATE users SET google_id = ?, email_verified_at = COALESCE(email_verified_at, ?) WHERE id = ?`,
			googleID, now, userID,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET google_id = ? WHERE id = ?`,
		googleID, userID,
	)
	return err
}

func (s *Store) VerifyEmail(ctx context.Context, token string) (User, error) {
	var userID string
	var expiresRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM email_verification_tokens WHERE token = ?`,
		token,
	).Scan(&userID, &expiresRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}

	expiresAt, err := parseStoredTime(expiresRaw)
	if err != nil {
		return User{}, err
	}
	if time.Now().UTC().After(expiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM email_verification_tokens WHERE token = ?`, token)
		return User{}, ErrNotFound
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET email_verified_at = ? WHERE id = ?`,
		now.Format(time.RFC3339), userID,
	)
	if err != nil {
		return User{}, err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM email_verification_tokens WHERE user_id = ?`, userID)

	return s.UserByID(ctx, userID)
}

func (s *Store) MarkEmailVerified(ctx context.Context, userID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET email_verified_at = ? WHERE id = ? AND email_verified_at IS NULL`,
		now.Format(time.RFC3339), userID,
	)
	return err
}

func (s *Store) AgentByUserID(ctx context.Context, userID string) (Agent, error) {
	return s.AgentByUserAndType(ctx, userID, "jobseeker")
}

func (s *Store) PublishAgent(ctx context.Context, userID string) (Agent, error) {
	return s.PublishAgentForType(ctx, userID, "jobseeker")
}

func (s *Store) PublishAgentForType(ctx context.Context, userID, agentType string) (Agent, error) {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return Agent{}, err
	}
	if user.EmailVerifiedAt == nil {
		return Agent{}, ErrEmailUnverified
	}

	agent, err := s.AgentByUserAndType(ctx, userID, agentType)
	if err != nil {
		return Agent{}, err
	}

	if agentType == agenttypes.Jobseeker || agentType == "" {
		hasResume := false
		if _, err := s.ResumeByUserID(ctx, userID); err == nil {
			hasResume = true
		}
		hasDocs, _ := s.HasAgentDocuments(ctx, agent.ID)
		if !agenttypes.ProfileComplete(agenttypes.Jobseeker, nil, hasResume, hasDocs) {
			return Agent{}, ErrResumeRequired
		}
	} else {
		profile := map[string]string{}
		if prof, err := s.AgentProfileByAgentID(ctx, agent.ID); err == nil {
			_ = json.Unmarshal([]byte(prof.ProfileJSON), &profile)
		}
		hasDocs, _ := s.HasAgentDocuments(ctx, agent.ID)
		if !agenttypes.ProfileComplete(agentType, profile, false, hasDocs) {
			return Agent{}, ErrProfileRequired
		}
	}

	if err := s.PublishAgentByID(ctx, agent.ID); err != nil {
		return Agent{}, err
	}
	return s.AgentByUserAndType(ctx, userID, agentType)
}

func (s *Store) EnsureAgent(ctx context.Context, id, userID, slug string) error {
	return s.CreateAgent(ctx, id, userID, "jobseeker", slug)
}

func (s *Store) CreateAgentAccessToken(ctx context.Context, agentID, token string, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM agent_access_tokens WHERE agent_id = ?`, agentID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_access_tokens (token, agent_id, expires_at) VALUES (?, ?, ?)`,
		token, agentID, expiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) ActiveAgentAccessToken(ctx context.Context, agentID string) (AgentAccessToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT token, agent_id, expires_at FROM agent_access_tokens WHERE agent_id = ? ORDER BY expires_at DESC LIMIT 1`,
		agentID,
	)
	var tok AgentAccessToken
	var expiresRaw string
	err := row.Scan(&tok.Token, &tok.AgentID, &expiresRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentAccessToken{}, ErrNotFound
		}
		return AgentAccessToken{}, err
	}
	tok.ExpiresAt, err = parseStoredTime( expiresRaw)
	if err != nil {
		return AgentAccessToken{}, err
	}
	if time.Now().UTC().After(tok.ExpiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM agent_access_tokens WHERE token = ?`, tok.Token)
		return AgentAccessToken{}, ErrAgentLinkExpired
	}
	return tok, nil
}

func (s *Store) AgentBySlugAndAccessToken(ctx context.Context, slug, token string) (Agent, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT a.id, a.user_id, a.type, a.slug, a.published, a.created_at, t.expires_at
FROM agents a
JOIN agent_access_tokens t ON t.agent_id = a.id
WHERE a.slug = ? AND t.token = ?`, slug, token)

	var a Agent
	var published int
	var createdRaw, expiresRaw string
	err := row.Scan(&a.ID, &a.UserID, &a.Type, &a.Slug, &published, &createdRaw, &expiresRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, err
	}
	if published != 1 {
		return Agent{}, ErrAgentUnpublished
	}
	expiresAt, err := parseStoredTime(expiresRaw)
	if err != nil {
		return Agent{}, err
	}
	if time.Now().UTC().After(expiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM agent_access_tokens WHERE token = ?`, token)
		return Agent{}, ErrAgentLinkExpired
	}
	a.Published = true
	a.CreatedAt, err = parseStoredTime( createdRaw)
	if err != nil {
		return Agent{}, err
	}
	return a, nil
}

func scanUser(row *sql.Row) (User, error) {
	var u User
	var verifiedRaw sql.NullString
	var googleRaw sql.NullString
	var createdRaw string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &googleRaw, &verifiedRaw, &createdRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	if googleRaw.Valid {
		u.GoogleID = googleRaw.String
	}
	if verifiedRaw.Valid {
		t, err := parseStoredTime(verifiedRaw.String)
		if err != nil {
			return User{}, err
		}
		u.EmailVerifiedAt = &t
	}
	u.CreatedAt, err = parseStoredTime(createdRaw)
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) AgentBySlug(ctx context.Context, slug string) (Agent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, type, slug, published, created_at FROM agents WHERE slug = ?`,
		slug,
	)
	return scanAgentRow(row)
}

type ResumeRecord struct {
	UserID            string
	Filename          string
	RawText           string
	IdentityVaultJSON string
	ExperienceContext string
	UploadedAt        time.Time
}

func (s *Store) UpsertResume(ctx context.Context, rec ResumeRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO resumes (user_id, filename, raw_text, identity_vault, experience_context, uploaded_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET
	filename = excluded.filename,
	raw_text = excluded.raw_text,
	identity_vault = excluded.identity_vault,
	experience_context = excluded.experience_context,
	uploaded_at = excluded.uploaded_at`,
		rec.UserID, rec.Filename, rec.RawText, rec.IdentityVaultJSON, rec.ExperienceContext,
		rec.UploadedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) ResumeByUserID(ctx context.Context, userID string) (ResumeRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT user_id, filename, raw_text, identity_vault, experience_context, uploaded_at FROM resumes WHERE user_id = ?`,
		userID,
	)
	var rec ResumeRecord
	var uploadedRaw string
	err := row.Scan(&rec.UserID, &rec.Filename, &rec.RawText, &rec.IdentityVaultJSON, &rec.ExperienceContext, &uploadedRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResumeRecord{}, ErrNotFound
		}
		return ResumeRecord{}, err
	}
	rec.UploadedAt, err = parseStoredTime( uploadedRaw)
	if err != nil {
		return ResumeRecord{}, err
	}
	return rec, nil
}

func (s *Store) ExperienceContextForAgent(ctx context.Context, agentID string) (string, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT r.experience_context FROM resumes r
JOIN agents a ON a.user_id = r.user_id
WHERE a.id = ?`, agentID)
	var ctxText string
	err := row.Scan(&ctxText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return ctxText, nil
}

func (s *Store) CreateChatSession(ctx context.Context, id, agentID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_sessions (id, agent_id, created_at) VALUES (?, ?, ?)`,
		id, agentID, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) ChatSessionByID(ctx context.Context, id string) (ChatSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, tokens_used, created_at FROM chat_sessions WHERE id = ?`, id,
	)
	var sess ChatSession
	var createdRaw string
	err := row.Scan(&sess.ID, &sess.AgentID, &sess.TokensUsed, &createdRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ChatSession{}, ErrNotFound
		}
		return ChatSession{}, err
	}
	sess.CreatedAt, err = parseStoredTime( createdRaw)
	if err != nil {
		return ChatSession{}, err
	}
	return sess, nil
}

func (s *Store) AddSessionTokens(ctx context.Context, sessionID string, tokens int) error {
	if tokens <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE chat_sessions SET tokens_used = tokens_used + ? WHERE id = ?`,
		tokens, sessionID,
	)
	return err
}

func (s *Store) CountUserMessages(ctx context.Context, sessionID string) (int, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chat_messages WHERE session_id = ? AND role = 'user'`, sessionID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) AppendChatMessage(ctx context.Context, id, sessionID, role, content string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_messages (id, session_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, sessionID, role, content, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) ListChatMessages(ctx context.Context, sessionID string) ([]ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, created_at FROM chat_messages WHERE session_id = ? ORDER BY created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var createdRaw string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &createdRaw); err != nil {
			return nil, err
		}
		m.CreatedAt, err = parseStoredTime( createdRaw)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *Store) ListSessionsForUser(ctx context.Context, userID string) ([]ChatSession, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT cs.id, cs.agent_id, cs.tokens_used, cs.created_at FROM chat_sessions cs
JOIN agents a ON a.id = cs.agent_id
WHERE a.user_id = ?
ORDER BY cs.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []ChatSession
	for rows.Next() {
		var sess ChatSession
		var createdRaw string
		if err := rows.Scan(&sess.ID, &sess.AgentID, &sess.TokensUsed, &createdRaw); err != nil {
			return nil, err
		}
		sess.CreatedAt, err = parseStoredTime( createdRaw)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func IsUniqueViolation(err error) bool { return isUniqueViolation(err) }
