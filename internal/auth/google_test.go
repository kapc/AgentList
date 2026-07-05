package auth_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chandresh/agentlist/internal/auth"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/email"
	"github.com/chandresh/agentlist/internal/store"
)

func TestGoogleOAuthEnabled(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		GoogleClientID:     "id.apps.googleusercontent.com",
		GoogleClientSecret: "secret",
		BaseURL:            "http://localhost:3000",
		JWTSecret:          []byte("test"),
		SessionTTL:         time.Hour,
	}
	if !cfg.GoogleOAuthEnabled() {
		t.Fatal("expected google oauth enabled")
	}

	g := auth.NewGoogleOAuth(cfg, nil, auth.NewTokenManager(cfg.JWTSecret, cfg.SessionTTL))
	if g == nil || !g.Enabled() {
		t.Fatal("expected google oauth client")
	}
	if g.RedirectURL() != "http://localhost:3000/api/v1/auth/google/callback" {
		t.Fatalf("unexpected redirect: %s", g.RedirectURL())
	}

	cfg.GoogleRedirectURI = "http://example.com/callback"
	g = auth.NewGoogleOAuth(cfg, nil, auth.NewTokenManager(cfg.JWTSecret, cfg.SessionTTL))
	if g.RedirectURL() != "http://example.com/callback" {
		t.Fatalf("expected custom redirect, got %s", g.RedirectURL())
	}
}

func TestGoogleLoginLinksExistingEmailAccount(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Config{
		BaseURL:           "http://localhost:3000",
		MinPasswordLength: 8,
		VerifyTokenTTL:    time.Hour,
		AgentLinkTTL:      time.Hour,
		SessionTTL:        time.Hour,
		JWTSecret:         []byte("test-secret"),
	}

	svc := auth.NewService(st, email.LogSender{}, auth.NewTokenManager(cfg.JWTSecret, cfg.SessionTTL), cfg)
	result, err := svc.Signup(context.Background(), "user@example.com", "password123")
	if err != nil {
		t.Fatal(err)
	}

	user, err := st.UserByID(context.Background(), result.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if user.GoogleID != "" {
		t.Fatal("expected no google id before link")
	}

	if err := st.LinkGoogleAccount(context.Background(), user.ID, "google-sub-123", true); err != nil {
		t.Fatal(err)
	}

	user, err = st.UserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.GoogleID != "google-sub-123" {
		t.Fatalf("expected google id linked, got %q", user.GoogleID)
	}
	if user.EmailVerifiedAt == nil {
		t.Fatal("expected email verified after google link")
	}
}
