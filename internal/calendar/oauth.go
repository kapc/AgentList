package calendar

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/store"
)

const oauthStateCookie = "agentlist_calendar_oauth_state"

var (
	ErrCalendarOAuthDisabled = errors.New("google calendar oauth not configured")
	ErrInvalidOAuthState     = errors.New("invalid oauth state")
)

type OAuth struct {
	cfg   config.Config
	store *store.Store
	oauth *oauth2.Config
}

func NewOAuth(cfg config.Config, st *store.Store) *OAuth {
	if !cfg.GoogleOAuthEnabled() {
		return nil
	}
	redirectURL := cfg.GoogleCalendarRedirectURI
	if redirectURL == "" {
		redirectURL = cfg.BaseURL + "/api/v1/calendar/google/callback"
	}
	return &OAuth{
		cfg:   cfg,
		store: st,
		oauth: &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  redirectURL,
			Scopes: []string{
				"https://www.googleapis.com/auth/calendar",
				"https://www.googleapis.com/auth/calendar.events",
			},
			Endpoint: google.Endpoint,
		},
	}
}

func (o *OAuth) Enabled() bool { return o != nil }

type connectState struct {
	Nonce     string `json:"n"`
	UserID    string `json:"u"`
	AgentType string `json:"t"`
}

func (o *OAuth) StartConnect(w http.ResponseWriter, r *http.Request, userID, agentType string) error {
	nonce, err := randomState()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(connectState{Nonce: nonce, UserID: userID, AgentType: agentType})
	if err != nil {
		return err
	}
	state := base64.RawURLEncoding.EncodeToString(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    nonce,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	url := o.oauth.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
	http.Redirect(w, r, url, http.StatusFound)
	return nil
}

func (o *OAuth) HandleCallback(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, error) {
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		return "", fmt.Errorf("google calendar auth denied: %s", errMsg)
	}
	stateRaw := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if stateRaw == "" || code == "" {
		return "", errors.New("missing oauth state or code")
	}

	stateBytes, err := base64.RawURLEncoding.DecodeString(stateRaw)
	if err != nil {
		return "", ErrInvalidOAuthState
	}
	var st connectState
	if err := json.Unmarshal(stateBytes, &st); err != nil {
		return "", ErrInvalidOAuthState
	}
	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != st.Nonce {
		return "", ErrInvalidOAuthState
	}
	clearOAuthStateCookie(w)

	tok, err := o.oauth.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", err)
	}
	if tok.RefreshToken == "" {
		return "", errors.New("google did not return a refresh token — revoke app access at myaccount.google.com/permissions and try again")
	}

	agent, err := o.store.AgentByUserAndType(ctx, st.UserID, st.AgentType)
	if err != nil {
		return "", err
	}

	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().UTC().Add(time.Hour)
	}
	if err := o.store.UpsertGoogleCalendarConnection(ctx, store.GoogleCalendarConnection{
		AgentID:      agent.ID,
		UserID:       st.UserID,
		CalendarID:   "primary",
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenExpiry:  expiry,
		ConnectedAt:  time.Now().UTC(),
	}); err != nil {
		return "", err
	}
	return st.AgentType, nil
}

func (o *OAuth) TokenSource(ctx context.Context, conn store.GoogleCalendarConnection) oauth2.TokenSource {
	tok := &oauth2.Token{
		AccessToken:  conn.AccessToken,
		RefreshToken: conn.RefreshToken,
		Expiry:       conn.TokenExpiry,
	}
	return o.oauth.TokenSource(ctx, tok)
}

func (o *OAuth) PersistRefreshedToken(ctx context.Context, agentID string, tok *oauth2.Token) error {
	if tok == nil || !tok.Valid() && tok.AccessToken == "" {
		return nil
	}
	conn, err := o.store.GoogleCalendarConnectionByAgentID(ctx, agentID)
	if err != nil {
		return err
	}
	conn.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		conn.RefreshToken = tok.RefreshToken
	}
	if !tok.Expiry.IsZero() {
		conn.TokenExpiry = tok.Expiry
	}
	return o.store.UpsertGoogleCalendarConnection(ctx, conn)
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func RedirectPath(agentType string) string {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		return "/dashboard"
	}
	return "/agents/" + agentType + "?calendar=connected"
}
