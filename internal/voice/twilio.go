package voice

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// ValidateTwilioRequest checks the X-Twilio-Signature header.
func ValidateTwilioRequest(authToken, signature, fullURL string, r *http.Request) bool {
	if authToken == "" || signature == "" {
		return false
	}
	if err := r.ParseForm(); err != nil {
		return false
	}
	params := make(map[string]string)
	for k, vals := range r.PostForm {
		if len(vals) > 0 {
			params[k] = vals[0]
		}
	}
	return validateSignature(authToken, signature, fullURL, params)
}

func validateSignature(authToken, signature, reqURL string, params map[string]string) bool {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(reqURL)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(params[k])
	}

	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(b.String()))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// RequestFullURL rebuilds the public URL Twilio signed against.
func RequestFullURL(r *http.Request, baseURL string) string {
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err == nil {
			path := r.URL.Path
			if r.URL.RawQuery != "" {
				path += "?" + r.URL.RawQuery
			}
			u.Path = ""
			u.RawQuery = ""
			// baseURL is scheme+host only usage
			full := strings.TrimRight(baseURL, "/") + path
			return full
		}
	}
	scheme := "https"
	if r.TLS == nil {
		if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
			scheme = fwd
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + r.Host + r.RequestURI
}
