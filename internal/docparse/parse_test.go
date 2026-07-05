package docparse

import "testing"

func TestFileExtFromMagicBytes(t *testing.T) {
	t.Parallel()
	if got := fileExt("", []byte("%PDF-1.4")); got != ".pdf" {
		t.Fatalf("pdf magic: got %q", got)
	}
	if got := fileExt("upload", []byte("%PDF-1.4")); got != ".pdf" {
		t.Fatalf("pdf with name: got %q", got)
	}
	if got := fileExt("doc.pdf", []byte("hello")); got != ".pdf" {
		t.Fatalf("ext from name: got %q", got)
	}
}
