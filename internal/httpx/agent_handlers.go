package httpx

import (
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/chandresh/agentlist/internal/agent"
	"github.com/chandresh/agentlist/internal/agenttypes"
	"github.com/chandresh/agentlist/internal/resume"
	"github.com/chandresh/agentlist/internal/store"
)

func (h *Handler) UploadResume(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}

	file, header, err := r.FormFile("resume")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing resume file")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 5<<20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read file")
		return
	}

	parsed, err := resume.ParseUploadWithOCR(r.Context(), header.Filename, data, h.agent.OpenAIAPIKey())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.UpsertResume(r.Context(), store.ResumeRecord{
		UserID:            user.ID,
		Filename:          header.Filename,
		RawText:           parsed.RawText,
		IdentityVaultJSON: parsed.IdentityVaultJSON,
		ExperienceContext: parsed.ExperienceContext,
		UploadedAt:        time.Now().UTC(),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save resume")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":            "resume uploaded",
		"filename":           header.Filename,
		"experience_preview": truncatePreview(parsed.ExperienceContext, 400),
	})
}

func (h *Handler) GetResume(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	rec, err := h.store.ResumeByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"uploaded": false})
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load resume")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"uploaded":           true,
		"filename":           rec.Filename,
		"uploaded_at":        rec.UploadedAt.UTC().Format(time.RFC3339),
		"experience_preview": truncatePreview(rec.ExperienceContext, 400),
	})
}

func (h *Handler) PublicAgentInfo(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	token := accessTokenFromRequest(r)
	if token == "" {
		writeError(w, http.StatusBadRequest, "missing access token")
		return
	}

	agentRow, err := h.store.AgentBySlugAndAccessToken(r.Context(), slug, token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		if errors.Is(err, store.ErrAgentLinkExpired) {
			writeError(w, http.StatusGone, "agent link has expired")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slug":        agentRow.Slug,
		"type":        agentRow.Type,
		"title":       agenttypes.TypeTitle(agentRow.Type),
		"intro":       agenttypes.PublicIntro(agentRow.Type),
		"disclaimer":  agenttypes.PublicDisclaimer(agentRow.Type),
		"token_limit": h.agent.SessionTokenLimit(),
	})
}

type chatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

func (h *Handler) AgentChat(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	token := accessTokenFromRequest(r)
	if token == "" {
		writeError(w, http.StatusBadRequest, "missing access token")
		return
	}

	var req chatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	result, err := h.agent.Chat(r.Context(), agent.ChatInput{
		AgentSlug:   slug,
		AccessToken: token,
		SessionID:   req.SessionID,
		Message:     req.Message,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrAgentUnpublished):
			writeError(w, http.StatusNotFound, "agent not found")
		case errors.Is(err, store.ErrAgentLinkExpired):
			writeError(w, http.StatusGone, "agent link has expired")
		case errors.Is(err, agent.ErrEmptyMessage):
			writeError(w, http.StatusBadRequest, "message cannot be empty")
		case errors.Is(err, store.ErrResumeRequired):
			writeError(w, http.StatusForbidden, "agent has no resume or documents yet")
		case errors.Is(err, store.ErrProfileRequired):
			writeError(w, http.StatusForbidden, "agent profile is incomplete")
		case errors.Is(err, store.ErrSessionTokenLimit):
			writeError(w, http.StatusTooManyRequests, "session token limit reached")
		case errors.Is(err, agent.ErrLLMNotConfigured):
			writeError(w, http.StatusServiceUnavailable, "agent is not configured (missing OPENAI_API_KEY)")
		case errors.Is(err, agent.ErrLLMQuotaExceeded):
			writeError(w, http.StatusServiceUnavailable, "OpenAI quota exceeded — add billing or credits at platform.openai.com")
		case errors.Is(err, agent.ErrLLMAuthFailed):
			writeError(w, http.StatusServiceUnavailable, "invalid OpenAI API key")
		default:
			writeError(w, http.StatusInternalServerError, "could not process message")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":       result.SessionID,
		"reply":            result.Reply,
		"tokens_used":      result.TokensUsed,
		"tokens_remaining": result.TokensRemaining,
		"token_limit":      result.TokenLimit,
	})
}

func (h *Handler) PublicAgentSession(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	token := accessTokenFromRequest(r)
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if token == "" {
		writeError(w, http.StatusBadRequest, "missing access token")
		return
	}
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "missing session_id")
		return
	}

	agentRow, err := h.store.AgentBySlugAndAccessToken(r.Context(), slug, token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		if errors.Is(err, store.ErrAgentLinkExpired) {
			writeError(w, http.StatusGone, "agent link has expired")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}

	sess, err := h.store.ChatSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load session")
		return
	}
	if sess.AgentID != agentRow.ID {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	limit := h.agent.SessionTokenLimit()
	remaining := limit - sess.TokensUsed
	if remaining < 0 {
		remaining = 0
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tokens_used":      sess.TokensUsed,
		"tokens_remaining": remaining,
		"token_limit":      limit,
		"limit_reached":    sess.TokensUsed >= limit,
	})
}

func (h *Handler) AgentPage(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	token := accessTokenFromRequest(r)
	if token == "" {
		http.NotFound(w, r)
		return
	}

	_, err := h.store.AgentBySlugAndAccessToken(r.Context(), slug, token)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data, err := readEmbeddedStatic("agent.html")
	if err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func readEmbeddedStatic(name string) ([]byte, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(sub, name)
}

func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	sessions, err := h.store.ListSessionsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load sessions")
		return
	}

	type item struct {
		ID         string `json:"id"`
		CreatedAt  string `json:"created_at"`
		TokensUsed int    `json:"tokens_used"`
	}
	out := make([]item, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, item{
			ID:         s.ID,
			CreatedAt:  s.CreatedAt.UTC().Format(time.RFC3339),
			TokensUsed: s.TokensUsed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token_limit": h.agent.SessionTokenLimit(),
		"sessions":    out,
	})
}

func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	sessionID := chi.URLParam(r, "sessionID")
	sess, err := h.store.ChatSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load session")
		return
	}

	agentRow, err := h.store.AgentByUserID(r.Context(), user.ID)
	if err != nil || agentRow.ID != sess.AgentID {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	msgs, err := h.store.ListChatMessages(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load messages")
		return
	}

	type msg struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]msg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, msg{
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

func truncatePreview(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func formFile(r *http.Request, names ...string) (multipart.File, *multipart.FileHeader, error) {
	for _, name := range names {
		if f, h, err := r.FormFile(name); err == nil {
			return f, h, nil
		}
	}
	return nil, nil, http.ErrMissingFile
}
