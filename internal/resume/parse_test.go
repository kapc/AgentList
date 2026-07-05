package resume

import (
	"strings"
	"testing"
)

func TestSplitIdentityRemovesContactInfo(t *testing.T) {
	t.Parallel()

	raw := `Jane Doe
jane.doe@example.com
(415) 555-0199
https://linkedin.com/in/janedoe

EXPERIENCE
Senior Engineer at Acme Corp
Built distributed systems in Go.`

	parsed, err := ParseUpload("resume.txt", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.IdentityVault.Email != "jane.doe@example.com" {
		t.Fatalf("email: got %q", parsed.IdentityVault.Email)
	}
	if parsed.IdentityVault.Name != "Jane Doe" {
		t.Fatalf("name: got %q", parsed.IdentityVault.Name)
	}
	if strings.Contains(parsed.ExperienceContext, "jane.doe@example.com") {
		t.Fatal("experience context must not contain email")
	}
	if !strings.Contains(parsed.ExperienceContext, "Senior Engineer") {
		t.Fatal("experience context should keep work history")
	}
}
