package httpx

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// cleanPageRoutes map extensionless paths to HTML files in the embedded static dir.
var cleanPageRoutes = map[string]string{
	"/":             "index.html",
	"/signup":       "signup.html",
	"/login":        "login.html",
	"/dashboard":    "dashboard.html",
	"/verify-email": "verify-email.html",
	"/agents/jobseeker": "agent-setup.html",
	"/agents/gardener":  "agent-setup.html",
	"/agents/recruiter": "agent-setup.html",
	"/agents/doctor":    "agent-setup.html",
}

func newStaticHandler(static fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		fileName, ok := resolveStaticFile(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}

		data, err := fs.ReadFile(static, fileName)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if ct := mime.TypeByExtension(path.Ext(fileName)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		// Avoid stale embedded assets during local development.
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	})
}

func resolveStaticFile(urlPath string) (string, bool) {
	if urlPath == "" {
		urlPath = "/"
	}
	if page, ok := cleanPageRoutes[urlPath]; ok {
		return page, true
	}

	name := strings.TrimPrefix(path.Clean(urlPath), "/")
	if name == "" || name == "." {
		return "index.html", true
	}
	if strings.Contains(name, "..") {
		return "", false
	}
	return name, true
}
