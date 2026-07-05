package docparse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

const MaxDocumentBytes = 5 << 20 // 5 MiB

var xmlTagRE = regexp.MustCompile(`<[^>]+>`)

// ExtractText pulls plain text from supported uploads.
// Image files (jpg, jpeg, png) require a non-empty openAIAPIKey for vision OCR.
func ExtractText(ctx context.Context, filename string, data []byte, openAIAPIKey string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty file")
	}
	if len(data) > MaxDocumentBytes {
		return "", fmt.Errorf("file exceeds 5 MB limit")
	}

	ext := fileExt(filename, data)
	switch ext {
	case ".txt", ".md", ".text":
		if !utf8.Valid(data) {
			return "", fmt.Errorf("file must be UTF-8 text")
		}
		return normalize(string(data)), nil
	case ".pdf":
		raw, err := extractPDF(data)
		if err != nil {
			return "", err
		}
		raw = normalize(raw)
		if strings.TrimSpace(raw) == "" && strings.TrimSpace(openAIAPIKey) != "" {
			if ocr, ocrErr := extractPDFWithVision(ctx, data, openAIAPIKey); ocrErr == nil && strings.TrimSpace(ocr) != "" {
				return normalize(ocr), nil
			}
		}
		if strings.TrimSpace(raw) == "" {
			return "", fmt.Errorf("could not extract text from PDF — if it is a scan, set OPENAI_API_KEY for OCR or upload as JPG/PNG")
		}
		return raw, nil
	case ".docx":
		raw, err := extractDocx(data)
		if err != nil {
			return "", err
		}
		return normalize(raw), nil
	case ".doc":
		raw, err := extractDoc(data)
		if err != nil {
			return "", err
		}
		return normalize(raw), nil
	case ".jpg", ".jpeg", ".png":
		if strings.TrimSpace(openAIAPIKey) == "" {
			return "", fmt.Errorf("image uploads require OPENAI_API_KEY for text extraction")
		}
		return extractImageText(ctx, openAIAPIKey, ext, data)
	default:
		return "", fmt.Errorf("unsupported file type %q (use .pdf, .doc, .docx, .jpg, .png, .txt, or .md)", ext)
	}
}

func extractPDF(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("read pdf: %w", err)
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func extractDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("read docx: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		xmlData, err := io.ReadAll(io.LimitReader(rc, MaxDocumentBytes))
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		text := xmlTagRE.ReplaceAllString(string(xmlData), " ")
		text = strings.ReplaceAll(text, "&amp;", "&")
		text = strings.ReplaceAll(text, "&lt;", "<")
		text = strings.ReplaceAll(text, "&gt;", ">")
		return text, nil
	}
	return "", fmt.Errorf("docx missing word/document.xml")
}

func extractDoc(data []byte) (string, error) {
	// Legacy .doc is OLE; best-effort printable ASCII extraction.
	var b strings.Builder
	for _, c := range string(data) {
		if c >= 32 && c < 127 || c == '\n' || c == '\r' || c == '\t' {
			b.WriteRune(c)
		}
	}
	raw := normalize(b.String())
	if len(raw) < 40 {
		return "", fmt.Errorf("could not extract text from .doc — save as .docx or .pdf")
	}
	return raw, nil
}

func extractImageText(ctx context.Context, apiKey, ext string, data []byte) (string, error) {
	mime := "image/jpeg"
	if ext == ".png" {
		mime = "image/png"
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	payload := map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "Extract all readable text from this document image. Return plain text only, preserving structure where possible. Do not add commentary."},
					{"type": "image_url", "image_url": map[string]string{
						"url": fmt.Sprintf("data:%s;base64,%s", mime, b64),
					}},
				},
			},
		},
		"max_tokens": 4000,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 90 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vision api %d", res.StatusCode)
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("no text found in image")
	}
	return normalize(parsed.Choices[0].Message.Content), nil
}

func normalize(s string) string {
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

// fileExt returns the extension from filename, or detects type from file magic bytes.
func fileExt(filename string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" {
		return ext
	}
	if len(data) >= 4 && string(data[:4]) == "%PDF" {
		return ".pdf"
	}
	if len(data) >= 4 && data[0] == 0x50 && data[1] == 0x4B && data[2] == 0x03 && data[3] == 0x04 {
		return ".docx"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return ".jpg"
	}
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return ".png"
	}
	return ext
}

func extractPDFWithVision(ctx context.Context, data []byte, apiKey string) (string, error) {
	// Scanned PDFs often embed JPEG streams; pull printable blocks and OCR via vision when needed.
	if jpeg := extractEmbeddedJPEG(data); len(jpeg) > 0 {
		return extractImageText(ctx, apiKey, ".jpg", jpeg)
	}
	return "", fmt.Errorf("no embedded images in pdf")
}

func extractEmbeddedJPEG(data []byte) []byte {
	// Find largest JFIF segment inside the PDF (typical for scanned pages).
	best := []byte(nil)
	for i := 0; i < len(data)-3; i++ {
		if data[i] == 0xFF && data[i+1] == 0xD8 && data[i+2] == 0xFF {
			end := i + 3
			for end < len(data)-1 {
				if data[end] == 0xFF && data[end+1] == 0xD9 {
					end += 2
					break
				}
				end++
			}
			candidate := data[i:end]
			if len(candidate) > len(best) {
				best = candidate
			}
		}
	}
	return best
}
