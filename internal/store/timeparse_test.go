package store

import (
	"testing"
	"time"
)

func TestParseStoredTime(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-07-02T18:40:50Z", time.Date(2026, 7, 2, 18, 40, 50, 0, time.UTC)},
		{"2026-07-04 05:59:42", time.Date(2026, 7, 4, 5, 59, 42, 0, time.UTC)},
	}
	for _, tc := range cases {
		got, err := parseStoredTime(tc.in)
		if err != nil {
			t.Fatalf("parseStoredTime(%q): %v", tc.in, err)
		}
		if !got.Equal(tc.want) {
			t.Fatalf("parseStoredTime(%q) = %v want %v", tc.in, got, tc.want)
		}
	}
}
