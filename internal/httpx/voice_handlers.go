package httpx

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/chandresh/agentlist/internal/store"
	"github.com/chandresh/agentlist/internal/voice"
)

func (h *Handler) VoiceConfig(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	agentType := chi.URLParam(r, "type")
	if !h.voice.SupportsVoice(agentType) {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}

	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{
				"supported": true,
				"enabled":   h.voice.Enabled(),
				"published": false,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}

	resp := map[string]any{
		"supported": true,
		"enabled":   h.voice.Enabled(),
		"published": agent.Published,
		"slug":      agent.Slug,
		"language":  h.voice.Locale().Language,
		"say_voice": h.voice.Locale().SayVoice,
	}
	if !h.voice.Enabled() {
		resp["message"] = "Set TWILIO_AUTH_TOKEN in .env and point a Twilio phone number at the webhook URL below."
		writeJSON(w, http.StatusOK, resp)
		return
	}

	link, err := h.auth.ActiveAgentLink(r.Context(), user.ID, agentType)
	if err != nil {
		resp["message"] = "Publish your agent first to get a voice webhook URL."
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp["webhook_url"] = h.voice.WebhookURL(agent.Slug, link.Token)
	resp["token_expires_at"] = link.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	resp["message"] = "In Twilio Console, set your phone number's Voice webhook to POST this URL. Use ngrok for local dev."
	writeJSON(w, http.StatusOK, resp)
}

// VoiceIncomingProbe answers Twilio/browser GET/HEAD URL checks (calls use POST).
func (h *Handler) VoiceIncomingProbe(w http.ResponseWriter, r *http.Request) {
	if !h.voice.Enabled() {
		http.Error(w, "phone agents not configured", http.StatusServiceUnavailable)
		return
	}
	slug := chi.URLParam(r, "slug")
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	if _, err := h.store.AgentBySlugAndAccessToken(r.Context(), slug, token); err != nil {
		http.Error(w, "invalid agent link", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("OK — Twilio voice webhook ready. Configure this URL as HTTP POST in Twilio, then call your Twilio number (do not open in a browser)."))
	}
}

func (h *Handler) VoiceIncoming(w http.ResponseWriter, r *http.Request) {
	if !h.voice.Enabled() {
		writeVoiceTwiML(w, voice.HangupTwiML("Phone agents are not configured on this server."))
		return
	}
	if !h.validateTwilio(r) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	slug := chi.URLParam(r, "slug")
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeVoiceTwiML(w, voice.HangupTwiML("Invalid agent link."))
		return
	}

	callSID := r.FormValue("CallSid")
	sessionID, agentRow, err := h.voice.EnsureCallSession(r.Context(), callSID, slug, token)
	if err != nil {
		writeVoiceTwiML(w, voice.HangupTwiML("This agent is not available."))
		return
	}

	twiml := h.voice.WelcomeTwiML(h.baseURL(), slug, token, sessionID, agentRow.Type)
	writeVoiceTwiML(w, twiml)
}

func (h *Handler) VoiceGather(w http.ResponseWriter, r *http.Request) {
	if !h.voice.Enabled() {
		writeVoiceTwiML(w, voice.HangupTwiML("Phone agents are not configured on this server."))
		return
	}
	if !h.validateTwilio(r) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	slug := chi.URLParam(r, "slug")
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))

	twiml, err := h.voice.HandleSpeech(r.Context(), voice.HandleInput{
		CallSID:      r.FormValue("CallSid"),
		Slug:         slug,
		AccessToken:  token,
		SessionID:    sessionID,
		SpeechResult: r.FormValue("SpeechResult"),
	})
	if err != nil {
		writeVoiceTwiML(w, voice.HangupTwiML("Something went wrong. Goodbye."))
		return
	}
	writeVoiceTwiML(w, twiml)
}

func (h *Handler) validateTwilio(r *http.Request) bool {
	if h.cfg.TwilioSkipSignature {
		return true
	}
	sig := r.Header.Get("X-Twilio-Signature")
	return voice.ValidateTwilioRequest(h.cfg.TwilioAuthToken, sig, voice.RequestFullURL(r, h.baseURL()), r)
}

func (h *Handler) baseURL() string {
	return h.cfg.BaseURL
}

func writeVoiceTwiML(w http.ResponseWriter, twiml string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(twiml))
}
