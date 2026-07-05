package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chandresh/agentlist/internal/agent"
	"github.com/chandresh/agentlist/internal/auth"
	"github.com/chandresh/agentlist/internal/calendar"
	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/connect"
	"github.com/chandresh/agentlist/internal/store"
	"github.com/chandresh/agentlist/internal/voice"
)

type AuthService interface {
	Signup(ctx context.Context, email, password string) (auth.SignupResult, error)
	Login(ctx context.Context, email, password string) (string, store.User, error)
	VerifyEmail(ctx context.Context, token string) (store.User, error)
	UserFromSession(token string) (store.User, error)
	PublishAgent(ctx context.Context, userID, agentType string) (auth.AgentLink, error)
	ActiveAgentLink(ctx context.Context, userID, agentType string) (auth.AgentLink, error)
	CreateAgent(ctx context.Context, userID, agentType string) (store.Agent, error)
	AgentsForUser(ctx context.Context, userID string) ([]store.Agent, error)
	AgentForUser(ctx context.Context, userID string) (store.Agent, error)
}

type Handler struct {
	auth                AuthService
	google              *auth.GoogleOAuth
	calendarOAuth       *calendar.OAuth
	calendar            *calendar.Service
	voice               *voice.Service
	cfg                 config.Config
	store               *store.Store
	agent               *agent.Service
	connect             *connect.Service
	devExposeVerifyLink bool
}

func NewHandler(authSvc AuthService, google *auth.GoogleOAuth, calendarOAuth *calendar.OAuth, calSvc *calendar.Service, voiceSvc *voice.Service, cfg config.Config, st *store.Store, agentSvc *agent.Service, connectSvc *connect.Service, devExposeVerifyLink bool) *Handler {
	return &Handler{
		auth:                authSvc,
		google:              google,
		calendarOAuth:       calendarOAuth,
		calendar:            calSvc,
		voice:               voiceSvc,
		cfg:                 cfg,
		store:               st,
		agent:               agentSvc,
		connect:             connectSvc,
		devExposeVerifyLink: devExposeVerifyLink,
	}
}

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID            string  `json:"id"`
	Email         string  `json:"email"`
	EmailVerified bool    `json:"email_verified"`
	GoogleLinked  bool    `json:"google_linked"`
	VerifiedAt    *string `json:"verified_at,omitempty"`
}

type agentResponse struct {
	Type          string  `json:"type,omitempty"`
	Slug          string  `json:"slug"`
	Link          string  `json:"link,omitempty"`
	LinkExpiresAt *string `json:"link_expires_at,omitempty"`
	Published     bool    `json:"published"`
}

type meResponse struct {
	User   userResponse  `json:"user"`
	Resume resumeStatus  `json:"resume"`
}

type resumeStatus struct {
	Uploaded bool   `json:"uploaded"`
	Filename string `json:"filename,omitempty"`
}

func (h *Handler) AuthConfig(w http.ResponseWriter, r *http.Request) {
	enabled := h.google != nil && h.google.Enabled()
	resp := map[string]any{
		"google_oauth_enabled": enabled,
	}
	if enabled {
		resp["google_redirect_uri"] = h.google.RedirectURL()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GoogleStart(w http.ResponseWriter, r *http.Request) {
	if h.google == nil || !h.google.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "Google sign-in is not configured")
		return
	}
	if err := h.google.Start(w, r); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start Google sign-in")
	}
}

func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	if h.google == nil || !h.google.Enabled() {
		http.Redirect(w, r, "/login?error=google_disabled", http.StatusFound)
		return
	}
	session, err := h.google.Callback(r.Context(), w, r)
	if err != nil {
		log.Printf("google oauth callback: %v", err)
		http.Redirect(w, r, "/login?error=google_failed", http.StatusFound)
		return
	}
	setSessionCookie(w, r, session)
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h *Handler) Signup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	result, err := h.auth.Signup(r.Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidEmail):
			writeError(w, http.StatusBadRequest, "invalid email address")
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, store.ErrEmailTaken):
			writeError(w, http.StatusConflict, "email already registered")
		default:
			writeError(w, http.StatusInternalServerError, "could not create account")
		}
		return
	}

	token, user, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account created but sign-in failed")
		return
	}
	setSessionCookie(w, r, token)

	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "account created",
		"user":    toUserResponse(user),
		"verification_link": devVerifyLink(h, result.VerifyURL),
	})
}

func devVerifyLink(h *Handler, url string) any {
	if h.devExposeVerifyLink && url != "" {
		return url
	}
	return nil
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	token, user, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, store.ErrInvalidCreds) {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"user": toUserResponse(user),
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	if h.devExposeVerifyLink && user.EmailVerifiedAt == nil {
		if err := h.store.MarkEmailVerified(r.Context(), user.ID); err == nil {
			if refreshed, err := h.store.UserByID(r.Context(), user.ID); err == nil {
				user = refreshed
			}
		}
	}

	resp := meResponse{User: toUserResponse(user)}
	if rec, err := h.store.ResumeByUserID(r.Context(), user.ID); err == nil {
		resp.Resume = resumeStatus{Uploaded: true, Filename: rec.Filename}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeError(w, http.StatusBadRequest, "missing token")
		return
	}

	user, err := h.auth.VerifyEmail(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "invalid or expired verification link")
			return
		}
		writeError(w, http.StatusInternalServerError, "verification failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "email verified",
		"user":    toUserResponse(user),
	})
}

func (h *Handler) PublishAgent(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	link, err := h.auth.PublishAgent(r.Context(), user.ID, "jobseeker")
	if err != nil {
		if errors.Is(err, store.ErrEmailUnverified) {
			writeError(w, http.StatusForbidden, "verify your email before publishing your agent link")
			return
		}
		if errors.Is(err, store.ErrResumeRequired) {
			writeError(w, http.StatusForbidden, "upload your resume before publishing your agent link")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not publish agent")
		return
	}

	base := schemeHost(r)
	exp := link.ExpiresAt.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, map[string]any{
		"agent": agentResponse{
			Slug:      link.Agent.Slug,
			Type:      link.Agent.Type,
			Published: true,
			Link:      buildAgentLink(base, link.Agent.Slug, link.Token),
			LinkExpiresAt: &exp,
		},
	})
}

func toUserResponse(u store.User) userResponse {
	resp := userResponse{
		ID:            u.ID,
		Email:         u.Email,
		EmailVerified: u.EmailVerifiedAt != nil,
		GoogleLinked:  u.GoogleID != "",
	}
	if u.EmailVerifiedAt != nil {
		s := u.EmailVerifiedAt.UTC().Format(time.RFC3339)
		resp.VerifiedAt = &s
	}
	return resp
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	cookie := &http.Cookie{
		Name:     auth.CookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	}
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func schemeHost(r *http.Request) string {
	if r.TLS != nil {
		return "https://" + r.Host
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd == "https" {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

func buildAgentLink(base, slug, token string) string {
	return base + "/a/" + slug + "?" + url.Values{"token": {token}}.Encode()
}

func accessTokenFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("token"))
}
