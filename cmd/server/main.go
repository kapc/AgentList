package main

import (
	"log"
	"net/http"

	"github.com/chandresh/agentlist/internal/agent"
	"github.com/chandresh/agentlist/internal/appointments"
	"github.com/chandresh/agentlist/internal/auth"
	"github.com/chandresh/agentlist/internal/calendar"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/connect"
	"github.com/chandresh/agentlist/internal/email"
	"github.com/chandresh/agentlist/internal/httpx"
	"github.com/chandresh/agentlist/internal/store"
	"github.com/chandresh/agentlist/internal/voice"
)

func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.SessionTTL)
	google := auth.NewGoogleOAuth(cfg, st, tokens)
	calendarOAuth := calendar.NewOAuth(cfg, st)
	calSvc := calendar.NewService(st, calendarOAuth)
	apptSvc := appointments.NewService(calSvc)
	mail := email.LogSender{}
	authSvc := auth.NewService(st, mail, tokens, cfg)
	agentSvc := agent.NewService(st, cfg, apptSvc)
	voiceSvc := voice.NewService(st, agentSvc, cfg)
	connectSvc := connect.NewService(st, agentSvc, mail, cfg)
	handler := httpx.NewHandler(authSvc, google, calendarOAuth, calSvc, voiceSvc, cfg, st, agentSvc, connectSvc, cfg.DevExposeVerifyLink)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: httpx.NewRouter(handler, tokens, st),
	}

	log.Printf("AgentList listening on %s (base URL %s)", cfg.Addr, cfg.BaseURL)
	log.Printf("Open %s/signup to create an account", cfg.BaseURL)
	if cfg.GoogleOAuthEnabled() {
		redirect := cfg.BaseURL + "/api/v1/auth/google/callback"
		if cfg.GoogleRedirectURI != "" {
			redirect = cfg.GoogleRedirectURI
		}
		log.Printf("Google OAuth enabled (redirect %s)", redirect)
		calRedirect := cfg.BaseURL + "/api/v1/calendar/google/callback"
		if cfg.GoogleCalendarRedirectURI != "" {
			calRedirect = cfg.GoogleCalendarRedirectURI
		}
		log.Printf("Google Calendar connect enabled (redirect %s)", calRedirect)
	} else {
		log.Printf("Google OAuth disabled — set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET in .env to enable")
	}
	if cfg.OpenAIAPIKey == "" {
		log.Printf("OPENAI_API_KEY not set — agent chat will return 503 until configured")
	}
	if cfg.TwilioVoiceEnabled() {
		log.Printf("Twilio voice enabled — configure phone webhook URLs in agent setup")
	} else {
		log.Printf("Twilio voice disabled — set TWILIO_AUTH_TOKEN in .env to enable phone agents")
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
