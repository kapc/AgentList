package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/chandresh/agentlist/internal/config"
	"github.com/chandresh/agentlist/internal/store"
)

const oauthStateCookie = "agentlist_oauth_state"

var ErrGoogleOAuthDisabled = errors.New("google oauth not configured")

type GoogleOAuth struct {
	cfg    config.Config
	store  *store.Store
	tokens *TokenManager
	oauth  *oauth2.Config
}

func NewGoogleOAuth(cfg config.Config, st *store.Store, tokens *TokenManager) *GoogleOAuth {
	if !cfg.GoogleOAuthEnabled() {
		return nil
	}
	redirectURL := cfg.GoogleRedirectURI
	if redirectURL == "" {
		redirectURL = cfg.BaseURL + "/api/v1/auth/google/callback"
	}
	return &GoogleOAuth{
		cfg:    cfg,
		store:  st,
		tokens: tokens,
		oauth: &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
	}
}

func (g *GoogleOAuth) Enabled() bool { return g != nil }

func (g *GoogleOAuth) RedirectURL() string {
	if g == nil || g.oauth == nil {
		return ""
	}
	return g.oauth.RedirectURL
}

func (g *GoogleOAuth) Start(w http.ResponseWriter, r *http.Request) error {
	state, err := randomState()
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	url := g.oauth.AuthCodeURL(state, oauth2.AccessTypeOnline, oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, url, http.StatusFound)
	return nil
}

func (g *GoogleOAuth) Callback(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, error) {
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		return "", fmt.Errorf("google auth denied: %s", errMsg)
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		return "", errors.New("missing oauth state or code")
	}

	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != state {
		return "", errors.New("invalid oauth state")
	}
	clearOAuthStateCookie(w)

	tok, err := g.oauth.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", err)
	}

	info, err := g.fetchGoogleUser(ctx, tok)
	if err != nil {
		return "", err
	}
	if info.Email == "" || info.ID == "" {
		return "", errors.New("google account missing email")
	}

	user, err := g.findOrCreateUser(ctx, info)
	if err != nil {
		return "", err
	}

	return g.tokens.Issue(user.ID, user.Email)
}

type googleProfile struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
}

func (g *GoogleOAuth) fetchGoogleUser(ctx context.Context, tok *oauth2.Token) (googleProfile, error) {
	client := g.oauth.Client(ctx, tok)
	res, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return googleProfile{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return googleProfile{}, fmt.Errorf("google userinfo %d: %s", res.StatusCode, string(body))
	}
	var info googleProfile
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return googleProfile{}, err
	}
	return info, nil
}

func (g *GoogleOAuth) findOrCreateUser(ctx context.Context, info googleProfile) (store.User, error) {
	if user, err := g.store.UserByGoogleID(ctx, info.ID); err == nil {
		return user, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, err
	}

	email, err := NormalizeEmail(info.Email)
	if err != nil {
		return store.User{}, err
	}

	if user, err := g.store.UserByEmail(ctx, email); err == nil {
		if user.GoogleID != "" && user.GoogleID != info.ID {
			return store.User{}, store.ErrEmailTaken
		}
		if err := g.store.LinkGoogleAccount(ctx, user.ID, info.ID, info.VerifiedEmail); err != nil {
			return store.User{}, err
		}
		return g.store.UserByID(ctx, user.ID)
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, err
	}

	userID := uuid.NewString()
	if err := g.store.CreateGoogleUser(ctx, userID, email, info.ID); err != nil {
		return store.User{}, err
	}
	return g.store.UserByID(ctx, userID)
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

func OAuthStateCookieName() string { return oauthStateCookie }
