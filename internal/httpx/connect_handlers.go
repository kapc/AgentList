package httpx

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/chandresh/agentlist/internal/connect"
	"github.com/chandresh/agentlist/internal/store"
)

type createConnectionRequest struct {
	SessionID        string `json:"session_id"`
	RecruiterName    string `json:"recruiter_name"`
	RecruiterCompany string `json:"recruiter_company"`
	RecruiterEmail   string `json:"recruiter_email"`
	RecruiterNote    string `json:"recruiter_note"`
}

func connectionRequestJSON(req store.ConnectionRequest) map[string]any {
	out := map[string]any{
		"id":                req.ID,
		"session_id":        req.SessionID,
		"status":            req.Status,
		"recruiter_name":    req.RecruiterName,
		"recruiter_company": req.RecruiterCompany,
		"recruiter_email":   req.RecruiterEmail,
		"recruiter_note":    req.RecruiterNote,
		"screening_summary": req.ScreeningSummary,
		"created_at":        req.CreatedAt.UTC().Format(time.RFC3339),
		"expires_at":        req.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if req.DecidedAt != nil {
		out["decided_at"] = req.DecidedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (h *Handler) PublicGetConnectionRequest(w http.ResponseWriter, r *http.Request) {
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

	req, err := h.connect.RequestForSession(r.Context(), slug, token, sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"connection_request": nil})
			return
		}
		if errors.Is(err, store.ErrAgentLinkExpired) {
			writeError(w, http.StatusGone, "agent link has expired")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load connection request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"connection_request": connectionRequestJSON(req),
	})
}

func (h *Handler) PublicCreateConnectionRequest(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	token := accessTokenFromRequest(r)
	if token == "" {
		writeError(w, http.StatusBadRequest, "missing access token")
		return
	}

	var body createConnectionRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	req, err := h.connect.CreateRequest(r.Context(), connect.CreateInput{
		AgentSlug:        slug,
		AccessToken:      token,
		SessionID:        body.SessionID,
		RecruiterName:    body.RecruiterName,
		RecruiterCompany: body.RecruiterCompany,
		RecruiterEmail:   body.RecruiterEmail,
		RecruiterNote:    body.RecruiterNote,
	})
	if err != nil {
		switch {
		case errors.Is(err, connect.ErrInvalidInput):
			writeError(w, http.StatusBadRequest, "company and session are required")
		case errors.Is(err, connect.ErrSessionEmpty):
			writeError(w, http.StatusBadRequest, "send at least one message before requesting an intro")
		case errors.Is(err, connect.ErrAlreadyRequested):
			writeError(w, http.StatusConflict, "connection request already submitted")
		case errors.Is(err, store.ErrConnectionExists):
			writeError(w, http.StatusConflict, "connection request already exists for this session")
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrAgentUnpublished):
			writeError(w, http.StatusNotFound, "agent not found")
		case errors.Is(err, store.ErrAgentLinkExpired):
			writeError(w, http.StatusGone, "agent link has expired")
		default:
			writeError(w, http.StatusInternalServerError, "could not create connection request")
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"connection_request": connectionRequestJSON(req),
		"message":            "Intro request sent to the candidate for approval.",
	})
}

func (h *Handler) ListConnectionRequests(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	pendingOnly := r.URL.Query().Get("pending") == "true"
	reqs, err := h.connect.ListForUser(r.Context(), user.ID, pendingOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load connection requests")
		return
	}

	out := make([]map[string]any, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, connectionRequestJSON(req))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection_requests": out})
}

func (h *Handler) ApproveConnectionRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	req, err := h.connect.Approve(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection request not found or already decided")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not approve connection request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"connection_request": connectionRequestJSON(req),
		"message":            "Approved — the recruiter will be notified.",
	})
}

func (h *Handler) DeclineConnectionRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	req, err := h.connect.Decline(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connection request not found or already decided")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not decline connection request")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"connection_request": connectionRequestJSON(req),
		"message":            "Declined — the recruiter will be notified if an email was provided.",
	})
}
