package appointments

import (
	"context"

	"github.com/chandresh/agentlist/internal/calendar"
	"github.com/chandresh/agentlist/internal/store"
)

// Service bridges chat tools to Google Calendar-backed scheduling.
type Service struct {
	calendar *calendar.Service
}

func NewService(cal *calendar.Service) *Service {
	return &Service{calendar: cal}
}

func (s *Service) SupportsScheduling(agentType string) bool {
	if s.calendar == nil {
		return false
	}
	return s.calendar.SupportsScheduling(agentType)
}

func (s *Service) SearchEarliest(ctx context.Context, agentID, agentType string, limit int) ([]store.AppointmentSlot, error) {
	slots, err := s.calendar.SearchEarliest(ctx, agentID, agentType, limit)
	if err != nil {
		return nil, err
	}
	out := make([]store.AppointmentSlot, 0, len(slots))
	for _, slot := range slots {
		out = append(out, store.AppointmentSlot{
			ID:          slot.ID,
			AgentID:     agentID,
			StartsAt:    slot.StartsAt,
			EndsAt:      slot.EndsAt,
			ServiceType: slot.ServiceType,
			Status:      "available",
		})
	}
	return out, nil
}

func (s *Service) Book(ctx context.Context, agentID string, in BookInput) (store.Appointment, store.AppointmentSlot, error) {
	result, err := s.calendar.Book(ctx, agentID, calendar.BookInput{
		SlotID:       in.SlotID,
		SessionID:    in.SessionID,
		PatientName:  in.PatientName,
		PatientEmail: in.PatientEmail,
		PatientPhone: in.PatientPhone,
		AgentType:    in.AgentType,
	})
	if err != nil {
		return store.Appointment{}, store.AppointmentSlot{}, err
	}
	slot := store.AppointmentSlot{
		ID:          result.Slot.ID,
		AgentID:     agentID,
		StartsAt:    result.Slot.StartsAt,
		EndsAt:      result.Slot.EndsAt,
		ServiceType: result.Slot.ServiceType,
		Status:      "booked",
	}
	return result.Appointment, slot, nil
}

type BookInput struct {
	SlotID       string
	SessionID    string
	PatientName  string
	PatientEmail string
	PatientPhone string
	AgentType    string
}
