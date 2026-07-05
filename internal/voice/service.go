package voice

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/chandresh/agentlist/internal/agent"
	"github.com/chandresh/agentlist/internal/agenttypes"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/store"
)

const maxSpeechReplyLen = 500

var (
	ErrVoiceDisabled = errors.New("twilio voice not configured")
	ErrInvalidToken  = errors.New("invalid voice access token")
)

type Service struct {
	store *store.Store
	agent *agent.Service
	cfg   config.Config
}

func NewService(st *store.Store, agentSvc *agent.Service, cfg config.Config) *Service {
	return &Service{store: st, agent: agentSvc, cfg: cfg}
}

func (s *Service) Enabled() bool {
	return s.cfg.TwilioAuthToken != ""
}

func (s *Service) SupportsVoice(agentType string) bool {
	return agentType == agenttypes.Doctor || agentType == agenttypes.Gardener
}

func (s *Service) WebhookURL(slug, accessToken string) string {
	return fmt.Sprintf("%s/api/v1/voice/agents/%s/incoming?token=%s",
		strings.TrimRight(s.cfg.BaseURL, "/"), slug, accessToken)
}

func (s *Service) EnsureCallSession(ctx context.Context, callSID, slug, accessToken string) (string, store.Agent, error) {
	if callSID == "" {
		return "", store.Agent{}, errors.New("missing call sid")
	}
	agentRow, err := s.store.AgentBySlugAndAccessToken(ctx, slug, accessToken)
	if err != nil {
		return "", store.Agent{}, ErrInvalidToken
	}
	if !agentRow.Published {
		return "", store.Agent{}, store.ErrAgentUnpublished
	}

	if existing, err := s.store.VoiceCallSessionByCallSID(ctx, callSID); err == nil {
		return existing.ChatSessionID, agentRow, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", store.Agent{}, err
	}

	sessionID, err := s.store.CreateVoiceCallSession(ctx, callSID, agentRow.ID)
	if err != nil {
		return "", store.Agent{}, err
	}
	return sessionID, agentRow, nil
}

type HandleInput struct {
	CallSID      string
	Slug         string
	AccessToken  string
	SessionID    string
	SpeechResult string
}

func (s *Service) HandleSpeech(ctx context.Context, in HandleInput) (string, error) {
	speech := strings.TrimSpace(in.SpeechResult)
	if speech == "" {
		return PromptTwiML(s.cfg.BaseURL, in.Slug, in.AccessToken, in.SessionID,
			"I did not catch that. Please repeat your question."), nil
	}

	result, err := s.agent.Chat(ctx, agent.ChatInput{
		AgentSlug:   in.Slug,
		AccessToken: in.AccessToken,
		SessionID:   in.SessionID,
		Message:     speech,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrSessionTokenLimit):
			return HangupTwiML("This call has reached the conversation limit. Goodbye."), nil
		case errors.Is(err, agent.ErrLLMNotConfigured):
			return HangupTwiML("This agent is not available right now. Please try again later."), nil
		default:
			return PromptTwiML(s.cfg.BaseURL, in.Slug, in.AccessToken, in.SessionID,
				"Sorry, something went wrong. Please try again."), nil
		}
	}

	reply := truncateForSpeech(result.Reply)
	if result.Booked != nil {
		reply = fmt.Sprintf("Your appointment is confirmed for %s.", result.Booked.Label)
	} else if len(result.Appointments) > 0 {
		var parts []string
		for i, appt := range result.Appointments {
			if i >= 5 {
				break
			}
			parts = append(parts, fmt.Sprintf("Option %d: %s", i+1, appt.Label))
		}
		if reply == "" {
			reply = "Here are the earliest available times. " + strings.Join(parts, ". ") +
				". Say which option you would like to book, and provide your name and email."
		}
	}

	return PromptTwiML(s.cfg.BaseURL, in.Slug, in.AccessToken, result.SessionID, reply), nil
}

func (s *Service) WelcomeTwiML(baseURL, slug, accessToken, sessionID, agentType string) string {
	greeting := agenttypes.PublicIntro(agentType)
	if agentType == agenttypes.Doctor {
		greeting = "Welcome. " + greeting + " You can ask about services, hours, or book an appointment."
	} else if agentType == agenttypes.Gardener {
		greeting = "Welcome. " + greeting + " You can ask about services or schedule a visit."
	}
	return PromptTwiML(baseURL, slug, accessToken, sessionID, greeting)
}

func truncateForSpeech(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxSpeechReplyLen {
		return s
	}
	return s[:maxSpeechReplyLen] + "..."
}

func PromptTwiML(baseURL, slug, token, sessionID, message string) string {
	action := fmt.Sprintf("%s/api/v1/voice/agents/%s/gather?token=%s&session_id=%s",
		strings.TrimRight(baseURL, "/"), slug, token, sessionID)
	return renderTwiML(message, action)
}

func HangupTwiML(message string) string {
	message = html.EscapeString(message)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="Polly.Joanna">%s</Say>
  <Hangup/>
</Response>`, message)
}

func renderTwiML(message, gatherAction string) string {
	message = html.EscapeString(message)
	gatherAction = html.EscapeString(gatherAction)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="Polly.Joanna">%s</Say>
  <Gather input="speech" action="%s" method="POST" speechTimeout="auto" language="en-US">
    <Say voice="Polly.Joanna">I'm listening.</Say>
  </Gather>
  <Say voice="Polly.Joanna">I did not hear anything. Goodbye.</Say>
  <Hangup/>
</Response>`, message, gatherAction)
}
