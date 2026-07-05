package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/chandresh/agentlist/internal/agenttypes"
	"github.com/chandresh/agentlist/internal/docparse"
	"github.com/chandresh/agentlist/internal/store"
)

func (h *Handler) ListAgentTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"types": agenttypes.All})
}

func (h *Handler) ListMyAgents(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	agents, err := h.auth.AgentsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load agents")
		return
	}

	byType := map[string]store.Agent{}
	for _, a := range agents {
		byType[a.Type] = a
	}

	out := make([]map[string]any, 0, len(agenttypes.All))
	for _, info := range agenttypes.All {
		item := map[string]any{
			"type":        info.ID,
			"title":       info.Title,
			"description": info.Description,
			"icon":        info.Icon,
			"created":     false,
		}
		if a, ok := byType[info.ID]; ok {
			item["created"] = true
			item["slug"] = a.Slug
			item["published"] = a.Published
			item["profile_complete"] = h.agentProfileComplete(r, user.ID, a)
			if a.Published {
				if link, err := h.auth.ActiveAgentLink(r.Context(), user.ID, a.Type); err == nil {
					item["link"] = buildAgentLink(schemeHost(r), link.Agent.Slug, link.Token)
					exp := link.ExpiresAt.UTC().Format(time.RFC3339)
					item["link_expires_at"] = exp
				}
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (h *Handler) CreateMyAgent(w http.ResponseWriter, r *http.Request) {
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

	agent, err := h.auth.CreateAgent(r.Context(), user.ID, agentType)
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "you already have this agent type")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create agent")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"agent": h.agentJSON(r, user.ID, agent),
	})
}

func (h *Handler) GetMyAgent(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	agentType := chi.URLParam(r, "type")
	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not created yet")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}

	resp := h.agentJSON(r, user.ID, agent)
	if agentType != agenttypes.Jobseeker {
		if prof, err := h.store.AgentProfileByAgentID(r.Context(), agent.ID); err == nil {
			var profile map[string]string
			_ = json.Unmarshal([]byte(prof.ProfileJSON), &profile)
			resp["profile"] = profile
			resp["fields"] = agenttypes.ProfileFields(agentType)
		} else {
			resp["profile"] = map[string]string{}
			resp["fields"] = agenttypes.ProfileFields(agentType)
		}
	}
	title, desc := agenttypes.PrimaryUploadInfo(agentType)
	resp["upload_title"] = title
	resp["upload_description"] = desc
	if docs, err := h.store.ListAgentDocuments(r.Context(), agent.ID); err == nil && len(docs) > 0 {
		resp["has_documents"] = true
		resp["document_count"] = len(docs)
		last := docs[len(docs)-1]
		resp["latest_document"] = map[string]any{
			"filename": last.Filename,
			"preview":  truncatePreview(last.RawText, 400),
		}
	} else {
		resp["has_documents"] = false
		resp["document_count"] = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": resp})
}

func (h *Handler) SaveAgentProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	agentType := chi.URLParam(r, "type")
	if !agenttypes.Valid(agentType) || agentType == agenttypes.Jobseeker {
		writeError(w, http.StatusBadRequest, "invalid agent type for profile")
		return
	}

	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "create the agent first")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}

	var profile map[string]string
	if err := decodeJSON(r, &profile); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if profile == nil {
		profile = map[string]string{}
	}
	for k, v := range profile {
		profile[k] = strings.TrimSpace(v)
	}

	raw, err := json.Marshal(profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save profile")
		return
	}
	contextText := agenttypes.BuildContext(agentType, profile, h.documentTexts(r.Context(), agent.ID))
	if err := h.store.UpsertAgentProfile(r.Context(), agent.ID, string(raw), contextText); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save profile")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":          "profile saved",
		"profile_complete": h.agentProfileComplete(r, user.ID, agent),
	})
}

func (h *Handler) UploadAgentDocument(w http.ResponseWriter, r *http.Request) {
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

	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if errors.Is(err, store.ErrNotFound) {
		agent, err = h.auth.CreateAgent(r.Context(), user.ID, agentType)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "create the agent first")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}

	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}
	file, header, err := formFile(r, "document", "file", "resume")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing document file")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, docparse.MaxDocumentBytes))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read file")
		return
	}

	text, err := docparse.ExtractText(r.Context(), header.Filename, data, h.agent.OpenAIAPIKey())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(text) == "" {
		writeError(w, http.StatusBadRequest, "no readable text found in file — try a different format or a clearer scan")
		return
	}

	docID := uuid.NewString()
	if err := h.store.CreateAgentDocument(r.Context(), docID, agent.ID, header.Filename, text); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save document")
		return
	}

	if err := h.syncAgentContext(r.Context(), agent); err != nil {
		// Document is saved; context sync is best-effort so upload still succeeds.
		_ = err
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":          "document uploaded",
		"id":               docID,
		"filename":         header.Filename,
		"preview":          truncatePreview(text, 400),
		"profile_complete": h.agentProfileComplete(r, user.ID, agent),
	})
}

func (h *Handler) ListAgentDocuments(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	agentType := chi.URLParam(r, "type")
	agent, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"documents": []any{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load agent")
		return
	}

	docs, err := h.store.ListAgentDocuments(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load documents")
		return
	}
	out := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		out = append(out, map[string]any{
			"id":          d.ID,
			"filename":    d.Filename,
			"uploaded_at": d.UploadedAt.UTC().Format(time.RFC3339),
			"preview":     truncatePreview(d.RawText, 200),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": out})
}

func (h *Handler) documentTexts(ctx context.Context, agentID string) []string {
	texts, err := h.store.DocumentTextsForAgent(ctx, agentID)
	if err != nil {
		return nil
	}
	return texts
}

func (h *Handler) syncAgentContext(ctx context.Context, agent store.Agent) error {
	if agent.Type == agenttypes.Jobseeker {
		return nil
	}
	profile := map[string]string{}
	if prof, err := h.store.AgentProfileByAgentID(ctx, agent.ID); err == nil {
		_ = json.Unmarshal([]byte(prof.ProfileJSON), &profile)
	}
	docTexts := h.documentTexts(ctx, agent.ID)
	contextText := agenttypes.BuildContext(agent.Type, profile, docTexts)
	raw, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	return h.store.UpsertAgentProfile(ctx, agent.ID, string(raw), contextText)
}

func (h *Handler) agentProfileComplete(r *http.Request, userID string, agent store.Agent) bool {
	hasDocs, _ := h.store.HasAgentDocuments(r.Context(), agent.ID)
	switch agent.Type {
	case agenttypes.Jobseeker:
		_, err := h.store.ResumeByUserID(r.Context(), userID)
		return agenttypes.ProfileComplete(agenttypes.Jobseeker, nil, err == nil, hasDocs)
	default:
		profile := map[string]string{}
		if prof, err := h.store.AgentProfileByAgentID(r.Context(), agent.ID); err == nil {
			_ = json.Unmarshal([]byte(prof.ProfileJSON), &profile)
		}
		return agenttypes.ProfileComplete(agent.Type, profile, false, hasDocs)
	}
}

func (h *Handler) PublishMyAgent(w http.ResponseWriter, r *http.Request) {
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

	if _, err := h.store.AgentByUserAndType(r.Context(), user.ID, agentType); errors.Is(err, store.ErrNotFound) {
		if _, err := h.auth.CreateAgent(r.Context(), user.ID, agentType); err != nil {
			writeError(w, http.StatusInternalServerError, "could not create agent")
			return
		}
	}

	link, err := h.auth.PublishAgent(r.Context(), user.ID, agentType)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrEmailUnverified):
			writeError(w, http.StatusForbidden, "verify your email before publishing")
		case errors.Is(err, store.ErrResumeRequired):
			writeError(w, http.StatusForbidden, "upload a resume or document before publishing")
		case errors.Is(err, store.ErrProfileRequired):
			writeError(w, http.StatusForbidden, "complete your profile or upload a document before publishing")
		default:
			writeError(w, http.StatusInternalServerError, "could not publish agent")
		}
		return
	}

	base := schemeHost(r)
	exp := link.ExpiresAt.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, map[string]any{
		"agent": agentResponse{
			Slug:          link.Agent.Slug,
			Type:          link.Agent.Type,
			Published:     true,
			Link:          buildAgentLink(base, link.Agent.Slug, link.Token),
			LinkExpiresAt: &exp,
		},
	})
}

func (h *Handler) agentJSON(r *http.Request, userID string, agent store.Agent) map[string]any {
	resp := map[string]any{
		"type":             agent.Type,
		"slug":             agent.Slug,
		"published":        agent.Published,
		"profile_complete": h.agentProfileComplete(r, userID, agent),
	}
	if agent.Published {
		if link, err := h.auth.ActiveAgentLink(r.Context(), userID, agent.Type); err == nil {
			resp["link"] = buildAgentLink(schemeHost(r), link.Agent.Slug, link.Token)
			exp := link.ExpiresAt.UTC().Format(time.RFC3339)
			resp["link_expires_at"] = exp
		}
	}
	return resp
}
