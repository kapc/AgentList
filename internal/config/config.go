package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr                string
	DatabasePath        string
	JWTSecret           []byte
	SessionTTL          time.Duration
	VerifyTokenTTL      time.Duration
	AgentLinkTTL        time.Duration
	BaseURL             string
	MinPasswordLength   int
	DevExposeVerifyLink bool
	GoogleClientID      string
	GoogleClientSecret  string
	GoogleRedirectURI   string
	OpenAIAPIKey        string
	OpenAIModel          string
	SessionTokenLimit    int
	ConnectionRequestTTL time.Duration
	ResumeStorageDir     string
}

func (c Config) GoogleOAuthEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func Load() Config {
	loadDotEnv(".env")

	sessionHours := envInt("SESSION_TTL_HOURS", 168)
	verifyHours := envInt("VERIFY_TOKEN_TTL_HOURS", 48)
	agentLinkHours := envInt("AGENT_LINK_TTL_HOURS", 1)
	connectionDays := envInt("CONNECTION_REQUEST_TTL_DAYS", 7)

	return Config{
		Addr:                env("ADDR", ":3000"),
		DatabasePath:        env("DATABASE_PATH", "agentlist.db"),
		JWTSecret:           []byte(env("JWT_SECRET", "dev-secret-change-in-production")),
		SessionTTL:          time.Duration(sessionHours) * time.Hour,
		VerifyTokenTTL:      time.Duration(verifyHours) * time.Hour,
		AgentLinkTTL:        time.Duration(agentLinkHours) * time.Hour,
		BaseURL:             env("BASE_URL", "http://localhost:3000"),
		MinPasswordLength:   envInt("MIN_PASSWORD_LENGTH", 8),
		DevExposeVerifyLink: env("DEV_EXPOSE_VERIFY_LINK", "true") == "true",
		GoogleClientID:      env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:  env("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURI:   env("GOOGLE_REDIRECT_URI", ""),
		OpenAIAPIKey:        env("OPENAI_API_KEY", ""),
		OpenAIModel:         env("OPENAI_MODEL", "gpt-4o-mini"),
		SessionTokenLimit:     envInt("SESSION_TOKEN_LIMIT", 20000),
		ConnectionRequestTTL:  time.Duration(connectionDays) * 24 * time.Hour,
		ResumeStorageDir:      env("RESUME_STORAGE_DIR", "./data/resumes"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
