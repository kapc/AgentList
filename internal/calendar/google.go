package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const calendarAPIBase = "https://www.googleapis.com/calendar/v3"

type Client struct {
	httpClient *http.Client
	calendarID string
}

func NewClient(ctx context.Context, tokenSource oauth2.TokenSource, calendarID string) *Client {
	if calendarID == "" {
		calendarID = "primary"
	}
	return &Client{
		httpClient: oauth2.NewClient(ctx, tokenSource),
		calendarID: calendarID,
	}
}

type busyPeriod struct {
	Start time.Time
	End   time.Time
}

func (c *Client) BusyPeriods(ctx context.Context, from, to time.Time) ([]busyPeriod, error) {
	body, err := json.Marshal(map[string]any{
		"timeMin": from.UTC().Format(time.RFC3339),
		"timeMax": to.UTC().Format(time.RFC3339),
		"items":   []map[string]string{{"id": c.calendarID}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, calendarAPIBase+"/freeBusy", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google freebusy %d: %s", res.StatusCode, truncate(string(respBody), 300))
	}

	var parsed struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	cal := parsed.Calendars[c.calendarID]
	out := make([]busyPeriod, 0, len(cal.Busy))
	for _, b := range cal.Busy {
		start, err := time.Parse(time.RFC3339, b.Start)
		if err != nil {
			continue
		}
		end, err := time.Parse(time.RFC3339, b.End)
		if err != nil {
			continue
		}
		out = append(out, busyPeriod{Start: start.UTC(), End: end.UTC()})
	}
	return out, nil
}

type EventInput struct {
	Summary     string
	Description string
	Start       time.Time
	End         time.Time
	AttendeeEmail string
}

type EventResult struct {
	ID      string
	HTMLLink string
}

func (c *Client) CreateEvent(ctx context.Context, in EventInput) (EventResult, error) {
	payload := map[string]any{
		"summary":     in.Summary,
		"description": in.Description,
		"start": map[string]string{
			"dateTime": in.Start.UTC().Format(time.RFC3339),
			"timeZone": "UTC",
		},
		"end": map[string]string{
			"dateTime": in.End.UTC().Format(time.RFC3339),
			"timeZone": "UTC",
		},
	}
	if email := strings.TrimSpace(in.AttendeeEmail); email != "" {
		payload["attendees"] = []map[string]any{{"email": email}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return EventResult{}, err
	}
	url := fmt.Sprintf("%s/calendars/%s/events?sendUpdates=all", calendarAPIBase, c.calendarID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return EventResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return EventResult{}, err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return EventResult{}, err
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return EventResult{}, fmt.Errorf("google create event %d: %s", res.StatusCode, truncate(string(respBody), 300))
	}
	var parsed struct {
		ID       string `json:"id"`
		HTMLLink string `json:"htmlLink"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return EventResult{}, err
	}
	return EventResult{ID: parsed.ID, HTMLLink: parsed.HTMLLink}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
