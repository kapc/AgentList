package httpx

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/chandresh/agentlist/internal/agenttypes"
	"github.com/chandresh/agentlist/internal/calendar"
	"github.com/chandresh/agentlist/internal/store"
)

func (h *Handler) CalendarConnect(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	agentType := chi.URLParam(r, "type")
	if !agenttypes.Valid(agentType) {
		writeError(w, http.StatusBadRequest, "unknown agent type")
		return
	}
	if agentType != agenttypes.Doctor && agentType != agenttypes.Gardener {
		writeError(w, http.StatusBadRequest, "this agent type does not use calendar scheduling")
		return
	}
	if h.calendarOAuth == nil || !h.calendarOAuth.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "Google Calendar is not configured")
		return
	}

	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			agent, err = h.auth.CreateAgent(r.Context(), user.ID, agentType)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load agent")
			return
		}
	}
	_ = agent

	if err := h.calendarOAuth.StartConnect(w, r, user.ID, agentType); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start Google Calendar connection")
	}
}

func (h *Handler) CalendarCallback(w http.ResponseWriter, r *http.Request) {
	if h.calendarOAuth == nil || !h.calendarOAuth.Enabled() {
		http.Redirect(w, r, "/dashboard?error=calendar_disabled", http.StatusFound)
		return
	}
	agentType, err := h.calendarOAuth.HandleCallback(r.Context(), w, r)
	if err != nil {
		http.Redirect(w, r, "/dashboard?error=calendar_failed", http.StatusFound)
		return
	}
	http.Redirect(w, r, calendar.RedirectPath(agentType), http.StatusFound)
}

func (h *Handler) CalendarStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	agentType := chi.URLParam(r, "type")
	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"connected": false})
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}
	connected, calID, err := h.calendar.ConnectionStatus(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load calendar status")
		return
	}
	resp := map[string]any{
		"connected":   connected,
		"calendar_id": calID,
	}
	if connected {
		if err := h.calendar.ValidateConnection(r.Context(), agent.ID); err != nil {
			resp["healthy"] = false
			if errors.Is(err, calendar.ErrScopeMissing) {
				resp["error"] = "missing_calendar_scope"
				resp["message"] = "Reconnect Google Calendar to grant appointment permissions."
			} else {
				resp["error"] = "calendar_unavailable"
				resp["message"] = err.Error()
			}
		} else {
			resp["healthy"] = true
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CalendarDisconnect(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	agentType := chi.URLParam(r, "type")
	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if err := h.calendar.Disconnect(r.Context(), agent.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not disconnect calendar")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Google Calendar disconnected"})
}
