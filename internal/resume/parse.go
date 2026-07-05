package resume

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/chandresh/agentlist/internal/docparse"
)

const maxResumeBytes = docparse.MaxDocumentBytes

var (
	emailRE    = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phoneRE    = regexp.MustCompile(`(?i)(?:\+?\d{1,3}[\s\-.]?)?(?:\(?\d{3}\)?[\s\-.]?)?\d{3}[\s\-.]?\d{4}`)
	linkedinRE = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?linkedin\.com/in/[a-z0-9\-_%]+/?`)
)

type IdentityVault struct {
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	Phone       string `json:"phone,omitempty"`
	LinkedInURL string `json:"linkedin_url,omitempty"`
}

type Parsed struct {
	RawText           string
	IdentityVault     IdentityVault
	IdentityVaultJSON string
	ExperienceContext string
}

func ParseUpload(filename string, data []byte) (Parsed, error) {
	return ParseUploadWithOCR(context.Background(), filename, data, "")
}

func ParseUploadWithOCR(ctx context.Context, filename string, data []byte, openAIAPIKey string) (Parsed, error) {
	if len(data) == 0 {
		return Parsed{}, fmt.Errorf("empty file")
	}
	if len(data) > maxResumeBytes {
		return Parsed{}, fmt.Errorf("file exceeds 5 MB limit")
	}

	raw, err := docparse.ExtractText(ctx, filename, data, openAIAPIKey)
	if err != nil {
		return Parsed{}, err
	}
	raw = normalizeText(raw)
	if strings.TrimSpace(raw) == "" {
		return Parsed{}, fmt.Errorf("could not extract text from file")
	}

	vault, experience := splitIdentity(raw)
	vaultJSON, err := json.Marshal(vault)
	if err != nil {
		return Parsed{}, err
	}

	return Parsed{
		RawText:           raw,
		IdentityVault:     vault,
		IdentityVaultJSON: string(vaultJSON),
		ExperienceContext: experience,
	}, nil
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func splitIdentity(raw string) (IdentityVault, string) {
	vault := IdentityVault{}
	lines := strings.Split(raw, "\n")
	kept := make([]string, 0, len(lines))

	for i, line := range lines {
		if vault.Email == "" {
			if m := emailRE.FindString(line); m != "" {
				vault.Email = m
			}
		}
		if vault.Phone == "" {
			if m := phoneRE.FindString(line); m != "" {
				vault.Phone = strings.TrimSpace(m)
			}
		}
		if vault.LinkedInURL == "" {
			if m := linkedinRE.FindString(line); m != "" {
				vault.LinkedInURL = m
			}
		}

		if i == 0 && vault.Name == "" && looksLikeName(line) {
			vault.Name = line
		}

		if isIdentityOnlyLine(line, vault) {
			continue
		}
		kept = append(kept, redactPII(line))
	}

	experience := strings.TrimSpace(strings.Join(kept, "\n"))
	if experience == "" {
		experience = redactPII(raw)
	}
	return vault, experience
}

func looksLikeName(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || len(line) > 60 {
		return false
	}
	if emailRE.MatchString(line) || phoneRE.MatchString(line) || linkedinRE.MatchString(line) {
		return false
	}
	words := strings.Fields(line)
	if len(words) < 2 || len(words) > 5 {
		return false
	}
	for _, w := range words {
		if len(w) < 2 {
			return false
		}
	}
	return true
}

func isIdentityOnlyLine(line string, vault IdentityVault) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	if vault.Name != "" && strings.EqualFold(trimmed, vault.Name) {
		return true
	}
	if vault.Email != "" && strings.Contains(trimmed, vault.Email) && len(trimmed) <= len(vault.Email)+10 {
		return true
	}
	if vault.Phone != "" && strings.Contains(trimmed, vault.Phone) {
		return true
	}
	if vault.LinkedInURL != "" && strings.Contains(strings.ToLower(trimmed), "linkedin.com") {
		return true
	}
	return false
}

func redactPII(line string) string {
	line = emailRE.ReplaceAllString(line, "[redacted email]")
	line = phoneRE.ReplaceAllString(line, "[redacted phone]")
	line = linkedinRE.ReplaceAllString(line, "[redacted linkedin]")
	return line
}
