package main

import (
	"log"
	"net/http"

	"github.com/chandresh/agentlist/internal/agent"
	"github.com/chandresh/agentlist/internal/auth"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/connect"
	"github.com/chandresh/agentlist/internal/email"
	"github.com/chandresh/agentlist/internal/httpx"
	"github.com/chandresh/agentlist/internal/store"
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
	mail := email.LogSender{}
	authSvc := auth.NewService(st, mail, tokens, cfg)
	agentSvc := agent.NewService(st, cfg)
	connectSvc := connect.NewService(st, agentSvc, mail, cfg)
	handler := httpx.NewHandler(authSvc, google, st, agentSvc, connectSvc, cfg.DevExposeVerifyLink)

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
	} else {
		log.Printf("Google OAuth disabled — set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET in .env to enable")
	}
	if cfg.OpenAIAPIKey == "" {
		log.Printf("OPENAI_API_KEY not set — agent chat will return 503 until configured")
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
