package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chandresh/agentlist/internal/appointments"
	"github.com/chandresh/agentlist/internal/calendar"
	"github.com/chandresh/agentlist/internal/store"
)

type chatExtras struct {
	appointments []store.AppointmentSlot
	booked       *store.Appointment
	bookedSlot   *store.AppointmentSlot
}

type AppointmentView struct {
	ID          string `json:"id"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	ServiceType string `json:"service_type"`
	Label       string `json:"label"`
}

type BookedAppointmentView struct {
	ID          string `json:"id"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	ServiceType string `json:"service_type"`
	PatientName string `json:"patient_name"`
	Label       string `json:"label"`
}

func toAppointmentViews(slots []store.AppointmentSlot) []AppointmentView {
	out := make([]AppointmentView, 0, len(slots))
	for _, slot := range slots {
		out = append(out, appointmentView(slot))
	}
	return out
}

func appointmentView(slot store.AppointmentSlot) AppointmentView {
	return AppointmentView{
		ID:          slot.ID,
		StartsAt:    slot.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:      slot.EndsAt.UTC().Format(time.RFC3339),
		ServiceType: slot.ServiceType,
		Label:       formatSlotLabel(slot),
	}
}

func toBookedView(appt *store.Appointment, slot *store.AppointmentSlot) *BookedAppointmentView {
	if appt == nil || slot == nil {
		return nil
	}
	return &BookedAppointmentView{
		ID:          appt.ID,
		StartsAt:    slot.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:      slot.EndsAt.UTC().Format(time.RFC3339),
		ServiceType: slot.ServiceType,
		PatientName: appt.PatientName,
		Label:       formatSlotLabel(*slot),
	}
}

func formatSlotLabel(slot store.AppointmentSlot) string {
	start := slot.StartsAt.UTC()
	end := slot.EndsAt.UTC()
	return fmt.Sprintf("%s, %s–%s (%s)",
		start.Format("Mon Jan 2, 2006"),
		start.Format("3:04 PM"),
		end.Format("3:04 PM"),
		slot.ServiceType,
	)
}

func appointmentTools() []openAITool {
	searchParams := json.RawMessage(`{
		"type": "object",
		"properties": {
			"limit": {
				"type": "integer",
				"description": "How many earliest available slots to return (default 5, max 20)"
			}
		}
	}`)
	bookParams := json.RawMessage(`{
		"type": "object",
		"properties": {
			"slot_id": { "type": "string", "description": "ID of the appointment slot to book" },
			"patient_name": { "type": "string", "description": "Patient or client full name" },
			"patient_email": { "type": "string", "description": "Email for confirmation (optional but recommended)" },
			"patient_phone": { "type": "string", "description": "Phone number (optional)" }
		},
		"required": ["slot_id", "patient_name"]
	}`)
	return []openAITool{
		{
			Type: "function",
			Function: openAIToolFunc{
				Name:        "search_appointment_slots",
				Description: "Find the earliest available appointment slots. Use when the user asks about availability, open times, or wants to book.",
				Parameters:  searchParams,
			},
		},
		{
			Type: "function",
			Function: openAIToolFunc{
				Name:        "book_appointment_slot",
				Description: "Book a specific appointment slot after the user chooses one and provides their name.",
				Parameters:  bookParams,
			},
		},
	}
}

func (s *Service) generateReplyWithTools(
	ctx context.Context,
	agentID, agentType, sessionID, experience string,
	history []store.ChatMessage,
	latest string,
	extras *chatExtras,
) (string, int, error) {
	if s.cfg.OpenAIAPIKey == "" {
		return "", 0, ErrLLMNotConfigured
	}
	if s.appointments == nil || !s.appointments.SupportsScheduling(agentType) {
		return s.generateReply(ctx, agentType, experience, history, latest)
	}

	msgs := []openAIMessage{
		{Role: "system", Content: strPtr(systemPrompt(agentType, experience))},
	}
	for _, m := range history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgs = append(msgs, openAIMessage{Role: m.Role, Content: strPtr(m.Content)})
	}
	msgs = append(msgs, openAIMessage{Role: "user", Content: strPtr(latest)})

	tools := appointmentTools()
	totalTokens := 0

	for round := 0; round < 5; round++ {
		body, err := json.Marshal(openAIRequest{
			Model:       s.cfg.OpenAIModel,
			Messages:    msgs,
			Tools:       tools,
			ToolChoice:  "auto",
			Temperature: 0.2,
			MaxTokens:   defaultMaxCompletionTokens,
		})
		if err != nil {
			return "", 0, err
		}

		parsed, tokens, err := s.callOpenAI(ctx, body)
		if err != nil {
			return "", 0, err
		}
		totalTokens += tokens

		if len(parsed.Choices) == 0 {
			return "", totalTokens, fmt.Errorf("empty model response")
		}
		choice := parsed.Choices[0].Message

		if len(choice.ToolCalls) == 0 {
			reply := strings.TrimSpace(deref(choice.Content))
			if reply == "" {
				return "", totalTokens, fmt.Errorf("empty model response")
			}
			return reply, totalTokens, nil
		}

		msgs = append(msgs, openAIMessage{
			Role:      "assistant",
			Content:   choice.Content,
			ToolCalls: choice.ToolCalls,
		})

		for _, call := range choice.ToolCalls {
			result, err := s.runAppointmentTool(ctx, agentID, agentType, sessionID, call, extras)
			if err != nil {
				result = jsonError(err.Error())
			}
			msgs = append(msgs, openAIMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    strPtr(result),
			})
		}
	}

	return "", totalTokens, fmt.Errorf("tool loop exceeded")
}

func (s *Service) runAppointmentTool(
	ctx context.Context,
	agentID, agentType, sessionID string,
	call openAIToolCall,
	extras *chatExtras,
) (string, error) {
	switch call.Function.Name {
	case "search_appointment_slots":
		var args struct {
			Limit int `json:"limit"`
		}
		if call.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		}
		if args.Limit <= 0 {
			args.Limit = 5
		}
		slots, err := s.appointments.SearchEarliest(ctx, agentID, agentType, args.Limit)
		if err != nil {
			return appointmentToolError(err), nil
		}
		extras.appointments = slots
		type slotJSON struct {
			ID          string `json:"id"`
			StartsAt    string `json:"starts_at"`
			EndsAt      string `json:"ends_at"`
			ServiceType string `json:"service_type"`
			Label       string `json:"label"`
		}
		out := make([]slotJSON, 0, len(slots))
		for _, slot := range slots {
			out = append(out, slotJSON{
				ID:          slot.ID,
				StartsAt:    slot.StartsAt.UTC().Format(time.RFC3339),
				EndsAt:      slot.EndsAt.UTC().Format(time.RFC3339),
				ServiceType: slot.ServiceType,
				Label:       formatSlotLabel(slot),
			})
		}
		b, err := json.Marshal(map[string]any{
			"slots": out,
			"count": len(out),
		})
		return string(b), err

	case "book_appointment_slot":
		var args struct {
			SlotID       string `json:"slot_id"`
			PatientName  string `json:"patient_name"`
			PatientEmail string `json:"patient_email"`
			PatientPhone string `json:"patient_phone"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return "", err
		}
		appt, slot, err := s.appointments.Book(ctx, agentID, appointments.BookInput{
			SlotID:       args.SlotID,
			SessionID:    sessionID,
			PatientName:  args.PatientName,
			PatientEmail: args.PatientEmail,
			PatientPhone: args.PatientPhone,
			AgentType:    agentType,
		})
		if err != nil {
			return appointmentToolError(err), nil
		}
		extras.booked = &appt
		extras.bookedSlot = &slot
		b, err := json.Marshal(map[string]any{
			"booked": true,
			"appointment_id": appt.ID,
			"slot_id": slot.ID,
			"label": formatSlotLabel(slot),
			"patient_name": appt.PatientName,
		})
		return string(b), err
	default:
		return jsonError("unknown tool: " + call.Function.Name), nil
	}
}

func appointmentToolError(err error) string {
	switch {
	case errors.Is(err, calendar.ErrNotConnected):
		return jsonError("calendar_not_connected: the practice owner has not connected Google Calendar yet")
	case errors.Is(err, calendar.ErrScopeMissing):
		return jsonError("calendar_scope_missing: reconnect Google Calendar in agent setup to enable booking")
	default:
		return jsonError(err.Error())
	}
}

func jsonError(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

func strPtr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
