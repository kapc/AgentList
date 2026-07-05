package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/chandresh/agentlist/internal/agenttypes"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/email"
	"github.com/chandresh/agentlist/internal/store"
)

type Service struct {
	store   *store.Store
	mail    email.Sender
	tokens  *TokenManager
	cfg     config.Config
}

func NewService(st *store.Store, mail email.Sender, tokens *TokenManager, cfg config.Config) *Service {
	return &Service{store: st, mail: mail, tokens: tokens, cfg: cfg}
}

type SignupResult struct {
	UserID    string
	Email     string
	VerifyURL string
}

func (s *Service) Signup(ctx context.Context, rawEmail, password string) (SignupResult, error) {
	emailAddr, err := NormalizeEmail(rawEmail)
	if err != nil {
		return SignupResult{}, err
	}
	if err := ValidatePassword(password, s.cfg.MinPasswordLength); err != nil {
		return SignupResult{}, err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return SignupResult{}, err
	}

	userID := uuid.NewString()
	if err := s.store.CreateUser(ctx, userID, emailAddr, hash); err != nil {
		return SignupResult{}, err
	}

	token, err := newRandomToken()
	if err != nil {
		return SignupResult{}, err
	}
	expires := time.Now().UTC().Add(s.cfg.VerifyTokenTTL)
	if err := s.store.CreateVerificationToken(ctx, token, userID, expires); err != nil {
		return SignupResult{}, err
	}

	verifyURL := fmt.Sprintf("%s/verify-email?token=%s", s.cfg.BaseURL, token)
	if s.cfg.DevExposeVerifyLink {
		if err := s.store.MarkEmailVerified(ctx, userID); err != nil {
			return SignupResult{}, err
		}
	} else if err := s.mail.SendVerification(ctx, emailAddr, verifyURL); err != nil {
		return SignupResult{}, err
	}

	return SignupResult{UserID: userID, Email: emailAddr, VerifyURL: verifyURL}, nil
}

func (s *Service) Login(ctx context.Context, rawEmail, password string) (string, store.User, error) {
	emailAddr, err := NormalizeEmail(rawEmail)
	if err != nil {
		return "", store.User{}, store.ErrInvalidCreds
	}

	user, err := s.store.UserByEmail(ctx, emailAddr)
	if err != nil {
		return "", store.User{}, store.ErrInvalidCreds
	}
	if user.PasswordHash == "" {
		return "", store.User{}, store.ErrInvalidCreds
	}
	if !CheckPassword(user.PasswordHash, password) {
		return "", store.User{}, store.ErrInvalidCreds
	}

	session, err := s.tokens.Issue(user.ID, user.Email)
	if err != nil {
		return "", store.User{}, err
	}
	return session, user, nil
}

func (s *Service) VerifyEmail(ctx context.Context, token string) (store.User, error) {
	return s.store.VerifyEmail(ctx, token)
}

func (s *Service) UserFromSession(token string) (store.User, error) {
	claims, err := s.tokens.Parse(token)
	if err != nil {
		return store.User{}, err
	}
	return s.store.UserByID(context.Background(), claims.UserID)
}

type AgentLink struct {
	Agent     store.Agent
	Token     string
	ExpiresAt time.Time
}

func (s *Service) PublishAgent(ctx context.Context, userID, agentType string) (AgentLink, error) {
	agent, err := s.store.PublishAgentForType(ctx, userID, agentType)
	if err != nil {
		return AgentLink{}, err
	}
	return s.issueAgentLink(ctx, agent)
}

func (s *Service) ActiveAgentLink(ctx context.Context, userID, agentType string) (AgentLink, error) {
	agent, err := s.store.AgentByUserAndType(ctx, userID, agentType)
	if err != nil {
		return AgentLink{}, err
	}
	if !agent.Published {
		return AgentLink{}, store.ErrNotFound
	}
	tok, err := s.store.ActiveAgentAccessToken(ctx, agent.ID)
	if err != nil {
		return AgentLink{}, err
	}
	return AgentLink{Agent: agent, Token: tok.Token, ExpiresAt: tok.ExpiresAt}, nil
}

func (s *Service) CreateAgent(ctx context.Context, userID, agentType string) (store.Agent, error) {
	if !agenttypes.Valid(agentType) {
		return store.Agent{}, store.ErrNotFound
	}
	if existing, err := s.store.AgentByUserAndType(ctx, userID, agentType); err == nil {
		return existing, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Agent{}, err
	}
	id := uuid.NewString()
	slug := agenttypes.SlugForUser(userID, agentType)
	if err := s.store.CreateAgent(ctx, id, userID, agentType, slug); err != nil {
		if errors.Is(err, store.ErrAgentExists) {
			return s.store.AgentByUserAndType(ctx, userID, agentType)
		}
		return store.Agent{}, err
	}
	return s.store.AgentByUserAndType(ctx, userID, agentType)
}

func (s *Service) AgentForUser(ctx context.Context, userID string) (store.Agent, error) {
	return s.store.AgentByUserID(ctx, userID)
}

func (s *Service) AgentsForUser(ctx context.Context, userID string) ([]store.Agent, error) {
	return s.store.ListAgentsByUser(ctx, userID)
}

func (s *Service) issueAgentLink(ctx context.Context, agent store.Agent) (AgentLink, error) {
	token, err := newRandomToken()
	if err != nil {
		return AgentLink{}, err
	}
	expires := time.Now().UTC().Add(s.cfg.AgentLinkTTL)
	if err := s.store.CreateAgentAccessToken(ctx, agent.ID, token, expires); err != nil {
		return AgentLink{}, err
	}
	return AgentLink{Agent: agent, Token: token, ExpiresAt: expires}, nil
}

func newRandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
