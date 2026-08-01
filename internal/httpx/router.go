package httpx

import (
	"context"
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/chandresh/agentlist/internal/auth"
	"github.com/chandresh/agentlist/internal/store"
)

//go:embed static/*
var staticFS embed.FS

func NewRouter(h *Handler, tokens *auth.TokenManager, st *store.Store) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	lookup := func(userID string) (store.User, error) {
		return st.UserByID(context.Background(), userID)
	}
	r.Use(AuthMiddleware(tokens, lookup))

	r.Get("/a/{slug}", h.AgentPage)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/auth/config", h.AuthConfig)
		r.Post("/auth/signup", h.Signup)
		r.Post("/auth/login", h.Login)
		r.Post("/auth/logout", h.Logout)
		r.Get("/auth/verify", h.VerifyEmail)
		r.Get("/auth/google", h.GoogleStart)
		r.Get("/auth/google/callback", h.GoogleCallback)
		r.Get("/calendar/google/callback", h.CalendarCallback)

		r.Get("/agent-types", h.ListAgentTypes)

		r.Get("/public/agents/{slug}", h.PublicAgentInfo)
		r.Get("/public/agents/{slug}/session", h.PublicAgentSession)
		r.Get("/public/agents/{slug}/connection-request", h.PublicGetConnectionRequest)
		r.Post("/public/agents/{slug}/connection-request", h.PublicCreateConnectionRequest)
		r.Post("/public/agents/{slug}/chat", h.AgentChat)

		r.Get("/voice/agents/{slug}/incoming", h.VoiceIncomingProbe)
		r.Head("/voice/agents/{slug}/incoming", h.VoiceIncomingProbe)
		r.Post("/voice/agents/{slug}/incoming", h.VoiceIncoming)
		r.Post("/voice/agents/{slug}/gather", h.VoiceGather)

		r.Group(func(r chi.Router) {
			r.Use(RequireAuth)
			r.Get("/me", h.Me)
			r.Get("/agents", h.ListMyAgents)
			r.Post("/agents/{type}", h.CreateMyAgent)
			r.Get("/agents/{type}", h.GetMyAgent)
			r.Put("/agents/{type}/profile", h.SaveAgentProfile)
			r.Post("/agents/{type}/documents", h.UploadAgentDocument)
			r.Get("/agents/{type}/documents", h.ListAgentDocuments)
			r.Post("/agents/{type}/publish", h.PublishMyAgent)
			r.Get("/agents/{type}/calendar/status", h.CalendarStatus)
			r.Get("/agents/{type}/calendar/connect", h.CalendarConnect)
			r.Delete("/agents/{type}/calendar", h.CalendarDisconnect)
			r.Get("/agents/{type}/voice/config", h.VoiceConfig)
			r.Post("/agent/publish", h.PublishAgent)
			r.Post("/resume", h.UploadResume)
			r.Get("/resume", h.GetResume)
			r.Get("/sessions", h.ListSessions)
			r.Get("/sessions/{sessionID}", h.GetSession)
			r.Get("/connection-requests", h.ListConnectionRequests)
			r.Post("/connection-requests/{id}/approve", h.ApproveConnectionRequest)
			r.Post("/connection-requests/{id}/decline", h.DeclineConnectionRequest)
		})
	})

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	staticHandler := newStaticHandler(static)
	r.NotFound(staticHandler.ServeHTTP)

	return r
}
