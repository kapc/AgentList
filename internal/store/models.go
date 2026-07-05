package store

import "time"

type User struct {
	ID              string
	Email           string
	PasswordHash    string
	GoogleID        string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
}

type Agent struct {
	ID        string
	UserID    string
	Type      string
	Slug      string
	Published bool
	CreatedAt time.Time
}

type AgentProfile struct {
	AgentID     string
	ProfileJSON string
	ContextText string
	UpdatedAt   time.Time
}

type EmailVerificationToken struct {
	Token     string
	UserID    string
	ExpiresAt time.Time
}

type AgentAccessToken struct {
	Token     string
	AgentID   string
	ExpiresAt time.Time
}

type Resume struct {
	UserID            string
	Filename          string
	ExperienceContext string
	UploadedAt        time.Time
}

type ChatSession struct {
	ID         string
	AgentID    string
	TokensUsed int
	CreatedAt  time.Time
}

type ChatMessage struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	CreatedAt time.Time
}
