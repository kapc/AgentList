package connect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chandresh/agentlist/internal/agent"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/email"
	"github.com/chandresh/agentlist/internal/store"
)

var (
	ErrInvalidInput     = errors.New("invalid connection request input")
	ErrSessionEmpty     = errors.New("screening session has no messages yet")
	ErrAlreadyRequested = errors.New("connection request already submitted")
)

type Service struct {
	store *store.Store
	agent *agent.Service
	mail  email.Sender
	cfg   config.Config
}

func NewService(st *store.Store, agentSvc *agent.Service, mail email.Sender, cfg config.Config) *Service {
	return &Service{store: st, agent: agentSvc, mail: mail, cfg: cfg}
}

type CreateInput struct {
	AgentSlug        string
	AccessToken      string
	SessionID        string
	RecruiterName    string
	RecruiterCompany string
	RecruiterEmail   string
	RecruiterNote    string
}

func (s *Service) CreateRequest(ctx context.Context, in CreateInput) (store.ConnectionRequest, error) {
	in.RecruiterName = strings.TrimSpace(in.RecruiterName)
	in.RecruiterCompany = strings.TrimSpace(in.RecruiterCompany)
	in.RecruiterEmail = strings.TrimSpace(strings.ToLower(in.RecruiterEmail))
	in.RecruiterNote = strings.TrimSpace(in.RecruiterNote)
	in.SessionID = strings.TrimSpace(in.SessionID)

	if in.SessionID == "" || in.RecruiterCompany == "" {
		return store.ConnectionRequest{}, ErrInvalidInput
	}

	agentRow, err := s.store.AgentBySlugAndAccessToken(ctx, in.AgentSlug, in.AccessToken)
	if err != nil {
		return store.ConnectionRequest{}, err
	}

	sess, err := s.store.ChatSessionByID(ctx, in.SessionID)
	if err != nil {
		return store.ConnectionRequest{}, err
	}
	if sess.AgentID != agentRow.ID {
		return store.ConnectionRequest{}, store.ErrNotFound
	}

	if existing, err := s.store.ConnectionRequestBySessionID(ctx, in.SessionID); err == nil {
		s.store.ExpireConnectionRequestIfNeeded(ctx, &existing)
		if existing.Status == store.ConnectionStatusPending || existing.Status == store.ConnectionStatusApproved {
			return existing, ErrAlreadyRequested
		}
		return existing, store.ErrConnectionExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.ConnectionRequest{}, err
	}

	msgs, err := s.store.ListChatMessages(ctx, in.SessionID)
	if err != nil {
		return store.ConnectionRequest{}, err
	}
	if len(msgs) == 0 {
		return store.ConnectionRequest{}, ErrSessionEmpty
	}

	summary, err := s.agent.SummarizeSession(ctx, msgs)
	if err != nil {
		summary = fallbackSummary(msgs, in.RecruiterNote)
	}

	now := time.Now().UTC()
	req := store.ConnectionRequest{
		ID:               uuid.NewString(),
		SessionID:        in.SessionID,
		AgentID:          agentRow.ID,
		Status:           store.ConnectionStatusPending,
		RecruiterName:    in.RecruiterName,
		RecruiterCompany: in.RecruiterCompany,
		RecruiterEmail:   in.RecruiterEmail,
		RecruiterNote:    in.RecruiterNote,
		ScreeningSummary: summary,
		CreatedAt:        now,
		ExpiresAt:        now.Add(s.cfg.ConnectionRequestTTL),
	}
	if err := s.store.CreateConnectionRequest(ctx, req); err != nil {
		return store.ConnectionRequest{}, err
	}

	candidate, err := s.store.UserByID(ctx, agentRow.UserID)
	if err != nil {
		return req, nil
	}
	dashboardURL := s.cfg.BaseURL + "/dashboard"
	_ = s.mail.SendConnectionRequest(ctx, candidate.Email, in.RecruiterCompany, summary, dashboardURL)

	return req, nil
}

func (s *Service) RequestForSession(ctx context.Context, slug, accessToken, sessionID string) (store.ConnectionRequest, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return store.ConnectionRequest{}, store.ErrNotFound
	}

	agentRow, err := s.store.AgentBySlugAndAccessToken(ctx, slug, accessToken)
	if err != nil {
		return store.ConnectionRequest{}, err
	}

	sess, err := s.store.ChatSessionByID(ctx, sessionID)
	if err != nil {
		return store.ConnectionRequest{}, err
	}
	if sess.AgentID != agentRow.ID {
		return store.ConnectionRequest{}, store.ErrNotFound
	}

	req, err := s.store.ConnectionRequestBySessionID(ctx, sessionID)
	if err != nil {
		return store.ConnectionRequest{}, err
	}
	s.store.ExpireConnectionRequestIfNeeded(ctx, &req)
	return req, nil
}

func (s *Service) ListForUser(ctx context.Context, userID string, pendingOnly bool) ([]store.ConnectionRequest, error) {
	if pendingOnly {
		reqs, err := s.store.ListConnectionRequestsForUser(ctx, userID, store.ConnectionStatusPending)
		if err != nil {
			return nil, err
		}
		for i := range reqs {
			s.store.ExpireConnectionRequestIfNeeded(ctx, &reqs[i])
		}
		var pending []store.ConnectionRequest
		for _, r := range reqs {
			if r.Status == store.ConnectionStatusPending {
				pending = append(pending, r)
			}
		}
		return pending, nil
	}
	reqs, err := s.store.ListConnectionRequestsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range reqs {
		s.store.ExpireConnectionRequestIfNeeded(ctx, &reqs[i])
	}
	return reqs, nil
}

func (s *Service) Approve(ctx context.Context, userID, requestID string) (store.ConnectionRequest, error) {
	req, err := s.authorizeRequest(ctx, userID, requestID)
	if err != nil {
		return store.ConnectionRequest{}, err
	}
	s.store.ExpireConnectionRequestIfNeeded(ctx, &req)
	if req.Status != store.ConnectionStatusPending {
		return store.ConnectionRequest{}, store.ErrNotFound
	}

	now := time.Now().UTC()
	if err := s.store.UpdateConnectionRequestStatus(ctx, requestID, store.ConnectionStatusApproved, now); err != nil {
		return store.ConnectionRequest{}, err
	}
	req.Status = store.ConnectionStatusApproved
	req.DecidedAt = &now

	if req.RecruiterEmail != "" {
		note := "The candidate approved your intro request. Calendar scheduling will be available soon — for now, watch for a follow-up email."
		_ = s.mail.SendConnectionApproved(ctx, req.RecruiterEmail, note)
	}

	return req, nil
}

func (s *Service) Decline(ctx context.Context, userID, requestID string) (store.ConnectionRequest, error) {
	req, err := s.authorizeRequest(ctx, userID, requestID)
	if err != nil {
		return store.ConnectionRequest{}, err
	}
	s.store.ExpireConnectionRequestIfNeeded(ctx, &req)
	if req.Status != store.ConnectionStatusPending {
		return store.ConnectionRequest{}, store.ErrNotFound
	}

	now := time.Now().UTC()
	if err := s.store.UpdateConnectionRequestStatus(ctx, requestID, store.ConnectionStatusDeclined, now); err != nil {
		return store.ConnectionRequest{}, err
	}
	req.Status = store.ConnectionStatusDeclined
	req.DecidedAt = &now

	if req.RecruiterEmail != "" {
		_ = s.mail.SendConnectionDeclined(ctx, req.RecruiterEmail)
	}

	return req, nil
}

func (s *Service) authorizeRequest(ctx context.Context, userID, requestID string) (store.ConnectionRequest, error) {
	req, err := s.store.ConnectionRequestByID(ctx, requestID)
	if err != nil {
		return store.ConnectionRequest{}, err
	}
	agentRow, err := s.store.AgentByUserID(ctx, userID)
	if err != nil || agentRow.ID != req.AgentID {
		return store.ConnectionRequest{}, store.ErrNotFound
	}
	return req, nil
}

func fallbackSummary(msgs []store.ChatMessage, note string) string {
	var b strings.Builder
	b.WriteString("Recruiter screening session summary:\n")
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		line := m.Content
		if len(line) > 200 {
			line = line[:200] + "…"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", m.Role, line)
	}
	if note != "" {
		fmt.Fprintf(&b, "\nRecruiter note: %s", note)
	}
	return b.String()
}
