package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/chandresh/agentlist/internal/agenttypes"
	"github.com/chandresh/agentlist/internal/store"
)

var (
	ErrNotConnected    = errors.New("google calendar is not connected for this agent")
	ErrScopeMissing    = errors.New("google calendar connection is missing permissions — reconnect in agent setup")
	ErrSlotUnavailable = errors.New("that appointment time is no longer available")
	ErrInvalidSlotID   = errors.New("invalid appointment slot")
)

type Slot struct {
	ID          string
	StartsAt    time.Time
	EndsAt      time.Time
	ServiceType string
}

type Service struct {
	store *store.Store
	oauth *OAuth
}

func NewService(st *store.Store, oauth *OAuth) *Service {
	return &Service{store: st, oauth: oauth}
}

func (s *Service) SupportsScheduling(agentType string) bool {
	return agentType == agenttypes.Doctor || agentType == agenttypes.Gardener
}

func (s *Service) ConnectionStatus(ctx context.Context, agentID string) (bool, string, error) {
	conn, err := s.store.GoogleCalendarConnectionByAgentID(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, conn.CalendarID, nil
}

// ValidateConnection checks that stored tokens can access the Calendar API.
func (s *Service) ValidateConnection(ctx context.Context, agentID string) error {
	client, _, err := s.clientForAgent(ctx, agentID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = client.BusyPeriods(ctx, now, now.Add(time.Hour))
	if err != nil {
		if strings.Contains(err.Error(), "insufficient authentication scopes") ||
			strings.Contains(err.Error(), "insufficientPermissions") {
			return ErrScopeMissing
		}
		return err
	}
	return nil
}

func (s *Service) Disconnect(ctx context.Context, agentID string) error {
	return s.store.DeleteGoogleCalendarConnection(ctx, agentID)
}

func (s *Service) clientForAgent(ctx context.Context, agentID string) (*Client, store.GoogleCalendarConnection, error) {
	if s.oauth == nil || !s.oauth.Enabled() {
		return nil, store.GoogleCalendarConnection{}, ErrNotConnected
	}
	conn, err := s.store.GoogleCalendarConnectionByAgentID(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, store.GoogleCalendarConnection{}, ErrNotConnected
		}
		return nil, store.GoogleCalendarConnection{}, err
	}
	ts := oauth2.ReuseTokenSource(nil, s.oauth.TokenSource(ctx, conn))
	tok, err := ts.Token()
	if err != nil {
		return nil, conn, fmt.Errorf("refresh google token: %w", err)
	}
	_ = s.oauth.PersistRefreshedToken(ctx, agentID, tok)
	conn.AccessToken = tok.AccessToken
	if !tok.Expiry.IsZero() {
		conn.TokenExpiry = tok.Expiry
	}
	return NewClient(ctx, ts, conn.CalendarID), conn, nil
}

func (s *Service) SearchEarliest(ctx context.Context, agentID, agentType string, limit int) ([]Slot, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	client, _, err := s.clientForAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	windowEnd := now.Add(21 * 24 * time.Hour)
	busy, err := client.BusyPeriods(ctx, now, windowEnd)
	if err != nil {
		return nil, err
	}

	serviceType := "consultation"
	if agentType == agenttypes.Gardener {
		serviceType = "service visit"
	}

	slots := computeFreeSlots(now, windowEnd, busy, 30*time.Minute, limit)
	out := make([]Slot, 0, len(slots))
	for _, slot := range slots {
		out = append(out, Slot{
			ID:          slot.Start.UTC().Format(time.RFC3339),
			StartsAt:    slot.Start.UTC(),
			EndsAt:      slot.End.UTC(),
			ServiceType: serviceType,
		})
	}
	return out, nil
}

type BookInput struct {
	SlotID       string
	SessionID    string
	PatientName  string
	PatientEmail string
	PatientPhone string
	AgentType    string
}

type BookResult struct {
	Appointment store.Appointment
	Slot        Slot
	CalendarURL string
}

func (s *Service) Book(ctx context.Context, agentID string, in BookInput) (BookResult, error) {
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(in.SlotID))
	if err != nil {
		return BookResult{}, ErrInvalidSlotID
	}
	end := start.Add(30 * time.Minute)

	client, _, err := s.clientForAgent(ctx, agentID)
	if err != nil {
		return BookResult{}, err
	}

	busy, err := client.BusyPeriods(ctx, start.Add(-time.Minute), end.Add(time.Minute))
	if err != nil {
		return BookResult{}, err
	}
	if overlapsBusy(start, end, busy) {
		return BookResult{}, ErrSlotUnavailable
	}

	name := strings.TrimSpace(in.PatientName)
	if name == "" {
		return BookResult{}, errors.New("patient name is required")
	}

	serviceType := "consultation"
	if in.AgentType == agenttypes.Gardener {
		serviceType = "service visit"
	}

	desc := fmt.Sprintf("Booked via AgentList chat.\nPatient: %s", name)
	if email := strings.TrimSpace(in.PatientEmail); email != "" {
		desc += "\nEmail: " + email
	}
	if phone := strings.TrimSpace(in.PatientPhone); phone != "" {
		desc += "\nPhone: " + phone
	}

	event, err := client.CreateEvent(ctx, EventInput{
		Summary:       fmt.Sprintf("%s — %s", serviceType, name),
		Description:   desc,
		Start:         start.UTC(),
		End:           end.UTC(),
		AttendeeEmail: strings.TrimSpace(in.PatientEmail),
	})
	if err != nil {
		return BookResult{}, err
	}

	appt := store.Appointment{
		ID:            uuid.NewString(),
		AgentID:       agentID,
		SessionID:     strings.TrimSpace(in.SessionID),
		GoogleEventID: event.ID,
		StartsAt:      start.UTC(),
		EndsAt:        end.UTC(),
		PatientName:   name,
		PatientEmail:  strings.TrimSpace(in.PatientEmail),
		PatientPhone:  strings.TrimSpace(in.PatientPhone),
		ServiceType:   serviceType,
		Status:        "confirmed",
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.store.CreateAppointment(ctx, appt); err != nil {
		return BookResult{}, err
	}

	slot := Slot{
		ID:          in.SlotID,
		StartsAt:    start.UTC(),
		EndsAt:      end.UTC(),
		ServiceType: serviceType,
	}
	return BookResult{Appointment: appt, Slot: slot, CalendarURL: event.HTMLLink}, nil
}

type interval struct {
	Start time.Time
	End   time.Time
}

func computeFreeSlots(from, to time.Time, busy []busyPeriod, duration time.Duration, limit int) []interval {
	var slots []interval
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	if from.Hour() >= 17 {
		day = day.Add(24 * time.Hour)
	}

	for d := 0; d < 21 && len(slots) < limit; d++ {
		date := day.Add(time.Duration(d) * 24 * time.Hour)
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		for hour := 9; hour < 17; hour++ {
			for _, minute := range []int{0, 30} {
				start := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, time.UTC)
				if !start.After(from) {
					continue
				}
				end := start.Add(duration)
				if end.After(to) {
					continue
				}
				if overlapsBusy(start, end, busy) {
					continue
				}
				slots = append(slots, interval{Start: start, End: end})
				if len(slots) >= limit {
					return slots
				}
			}
		}
	}
	return slots
}

func overlapsBusy(start, end time.Time, busy []busyPeriod) bool {
	for _, b := range busy {
		if start.Before(b.End) && end.After(b.Start) {
			return true
		}
	}
	return false
}
