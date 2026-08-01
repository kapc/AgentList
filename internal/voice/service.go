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
	store  *store.Store
	agent  *agent.Service
	cfg    config.Config
	locale Locale
}

func NewService(st *store.Store, agentSvc *agent.Service, cfg config.Config) *Service {
	return &Service{store: st, agent: agentSvc, cfg: cfg, locale: LocaleFromConfig(cfg)}
}

func (s *Service) Enabled() bool {
	return s.cfg.TwilioAuthToken != ""
}

func (s *Service) Locale() Locale {
	return s.locale
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
	p := s.locale.Phrases()
	speech := strings.TrimSpace(in.SpeechResult)
	if speech == "" {
		return s.PromptTwiML(s.cfg.BaseURL, in.Slug, in.AccessToken, in.SessionID, p.DidNotCatch), nil
	}

	result, err := s.agent.Chat(ctx, agent.ChatInput{
		AgentSlug:     in.Slug,
		AccessToken:   in.AccessToken,
		SessionID:     in.SessionID,
		Message:       speech,
		ReplyLanguage: s.locale.ReplyLang,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrSessionTokenLimit):
			return s.HangupTwiML(p.LimitReached), nil
		case errors.Is(err, agent.ErrLLMNotConfigured):
			return s.HangupTwiML(p.Unavailable), nil
		default:
			return s.PromptTwiML(s.cfg.BaseURL, in.Slug, in.AccessToken, in.SessionID, p.SorryRetry), nil
		}
	}

	reply := truncateForSpeech(result.Reply)
	if result.Booked != nil {
		reply = fmt.Sprintf(p.BookedFmt, result.Booked.Label)
	} else if len(result.Appointments) > 0 && s.locale.ReplyLang == "" {
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

	return s.PromptTwiML(s.cfg.BaseURL, in.Slug, in.AccessToken, result.SessionID, reply), nil
}

func (s *Service) WelcomeTwiML(baseURL, slug, accessToken, sessionID, agentType string) string {
	p := s.locale.Phrases()
	var greeting string
	switch {
	case agentType == agenttypes.Doctor && p.DoctorWelcome != "":
		greeting = p.DoctorWelcome
	case agentType == agenttypes.Gardener && p.GardenWelcome != "":
		greeting = p.GardenWelcome
	case agentType == agenttypes.Doctor:
		greeting = "Welcome. " + agenttypes.PublicIntro(agentType) + " You can ask about services, hours, or book an appointment."
	case agentType == agenttypes.Gardener:
		greeting = "Welcome. " + agenttypes.PublicIntro(agentType) + " You can ask about services or schedule a visit."
	default:
		greeting = agenttypes.PublicIntro(agentType)
	}
	return s.PromptTwiML(baseURL, slug, accessToken, sessionID, greeting)
}

func truncateForSpeech(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxSpeechReplyLen {
		return s
	}
	return s[:maxSpeechReplyLen] + "..."
}

func (s *Service) PromptTwiML(baseURL, slug, token, sessionID, message string) string {
	action := fmt.Sprintf("%s/api/v1/voice/agents/%s/gather?token=%s&session_id=%s",
		strings.TrimRight(baseURL, "/"), slug, token, sessionID)
	return s.renderTwiML(message, action)
}

func (s *Service) HangupTwiML(message string) string {
	message = html.EscapeString(message)
	voice := html.EscapeString(s.locale.SayVoice)
	lang := html.EscapeString(s.locale.Language)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="%s" language="%s">%s</Say>
  <Hangup/>
</Response>`, voice, lang, message)
}

func (s *Service) renderTwiML(message, gatherAction string) string {
	p := s.locale.Phrases()
	message = html.EscapeString(message)
	gatherAction = html.EscapeString(gatherAction)
	voice := html.EscapeString(s.locale.SayVoice)
	lang := html.EscapeString(s.locale.Language)
	listening := html.EscapeString(p.ListeningHint)
	noInput := html.EscapeString(p.NoInput)

	gatherAttrs := fmt.Sprintf(`input="speech" action="%s" method="POST" language="%s"`, gatherAction, lang)
	// Twilio forbids speechTimeout="auto" when speechModel is set (error 13335).
	if s.locale.SpeechModel != "" {
		gatherAttrs += fmt.Sprintf(` speechTimeout="2" speechModel="%s"`, html.EscapeString(s.locale.SpeechModel))
	} else {
		gatherAttrs += ` speechTimeout="auto"`
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="%s" language="%s">%s</Say>
  <Gather %s>
    <Say voice="%s" language="%s">%s</Say>
  </Gather>
  <Say voice="%s" language="%s">%s</Say>
  <Hangup/>
</Response>`, voice, lang, message, gatherAttrs, voice, lang, listening, voice, lang, noInput)
}

// Package-level helpers used by HTTP handlers for static errors before Service methods.
func HangupTwiML(message string) string {
	message = html.EscapeString(message)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="Polly.Joanna">%s</Say>
  <Hangup/>
</Response>`, message)
}
