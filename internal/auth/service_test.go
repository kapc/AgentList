package auth_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chandresh/agentlist/internal/auth"
	"github.com/chandresh/agentlist/internal/agenttypes"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/email"
	"github.com/chandresh/agentlist/internal/store"
)

func TestSignupVerifyPublishGate(t *testing.T) {
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

	var lastVerifyURL string
	mail := email.FuncSender(func(_ context.Context, _, verifyURL string) error {
		lastVerifyURL = verifyURL
		return nil
	})

	svc := auth.NewService(st, mail, auth.NewTokenManager(cfg.JWTSecret, cfg.SessionTTL), cfg)

	result, err := svc.Signup(context.Background(), "Applicant@Example.com", "password123")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if result.Email != "applicant@example.com" {
		t.Fatalf("expected normalized email, got %q", result.Email)
	}

	_, err = svc.PublishAgent(context.Background(), result.UserID, agenttypes.Jobseeker)
	if err != store.ErrEmailUnverified {
		t.Fatalf("expected ErrEmailUnverified before verify, got %v", err)
	}

	token := extractToken(lastVerifyURL)
	if token == "" {
		t.Fatal("expected verification URL with token")
	}

	user, err := svc.VerifyEmail(context.Background(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if user.EmailVerifiedAt == nil {
		t.Fatal("expected email_verified_at to be set")
	}

	_, err = svc.CreateAgent(context.Background(), result.UserID, agenttypes.Jobseeker)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	_, err = svc.PublishAgent(context.Background(), result.UserID, agenttypes.Jobseeker)
	if err != store.ErrResumeRequired {
		t.Fatalf("expected ErrResumeRequired before resume upload, got %v", err)
	}

	if err := st.UpsertResume(context.Background(), store.ResumeRecord{
		UserID:            result.UserID,
		Filename:          "resume.txt",
		RawText:           "Jane Doe\nSenior Engineer at Acme",
		IdentityVaultJSON: `{}`,
		ExperienceContext: "Senior Engineer at Acme",
		UploadedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert resume: %v", err)
	}

	_, err = svc.CreateAgent(context.Background(), result.UserID, agenttypes.Jobseeker)
	if err != nil {
		t.Fatalf("create jobseeker agent: %v", err)
	}

	link, err := svc.PublishAgent(context.Background(), result.UserID, agenttypes.Jobseeker)
	if err != nil {
		t.Fatalf("publish after verify: %v", err)
	}
	if !link.Agent.Published {
		t.Fatal("expected agent published")
	}
	if link.Token == "" {
		t.Fatal("expected access token")
	}
	if link.ExpiresAt.Before(time.Now().UTC()) {
		t.Fatal("expected link expiry in the future")
	}
}

func extractToken(url string) string {
	const key = "token="
	i := len(url) - 1
	for i >= 0 && url[i] != '?' && url[i] != '&' {
		i--
	}
	start := i + 1
	if start+len(key) > len(url) || url[start:start+len(key)] != key {
		return ""
	}
	return url[start+len(key):]
}
