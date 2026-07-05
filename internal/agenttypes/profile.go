package agenttypes

import "strings"

type FieldDef struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Required    bool   `json:"required"`
	Multiline   bool   `json:"multiline"`
}

func ProfileFields(agentType string) []FieldDef {
	switch agentType {
	case Gardener:
		return []FieldDef{
			{Key: "business_name", Label: "Business name", Placeholder: "Green Thumb Landscaping"},
			{Key: "services", Label: "Services offered", Placeholder: "Lawn mowing, hedge trimming, seasonal cleanup…", Required: true, Multiline: true},
			{Key: "pricing", Label: "Pricing & charges", Placeholder: "$50/hour · $120 full yard mow · free estimates", Required: true, Multiline: true},
			{Key: "service_area", Label: "Service area", Placeholder: "San Jose, CA — within 15 miles"},
			{Key: "availability", Label: "Availability", Placeholder: "Weekdays & Saturdays, 8am–5pm"},
			{Key: "specialties", Label: "Specialties", Placeholder: "Drought-tolerant gardens, organic lawn care", Multiline: true},
		}
	case Recruiter:
		return []FieldDef{
			{Key: "company_name", Label: "Company name", Placeholder: "Acme Labs", Required: true},
			{Key: "roles_hiring", Label: "Open roles", Placeholder: "Senior backend engineer (Go), product manager…", Required: true, Multiline: true},
			{Key: "hiring_process", Label: "Hiring process", Placeholder: "Recruiter screen → technical → team fit → offer", Multiline: true},
			{Key: "company_blurb", Label: "About the company", Placeholder: "What you build, team size, culture…", Multiline: true},
			{Key: "benefits", Label: "Benefits & perks", Placeholder: "Remote-friendly, equity, learning budget…", Multiline: true},
		}
	case Doctor:
		return []FieldDef{
			{Key: "practice_name", Label: "Practice / clinic name", Placeholder: "Bay Area Family Medicine"},
			{Key: "specialty", Label: "Specialty", Placeholder: "Family medicine, dermatology, pediatrics…", Required: true},
			{Key: "services", Label: "Services offered", Placeholder: "Annual physicals, telehealth visits, minor procedures…", Required: true, Multiline: true},
			{Key: "consultation_fees", Label: "Consultation fees", Placeholder: "$200 new patient · $120 follow-up · accepts insurance", Multiline: true},
			{Key: "office_hours", Label: "Office hours", Placeholder: "Mon–Fri 9am–5pm, Sat 10am–2pm"},
			{Key: "insurance_accepted", Label: "Insurance accepted", Placeholder: "Aetna, Blue Cross, Medicare…", Multiline: true},
			{Key: "languages", Label: "Languages spoken", Placeholder: "English, Spanish"},
			{Key: "location_area", Label: "Location / area served", Placeholder: "Downtown San Francisco · telehealth statewide"},
		}
	default:
		return nil
	}
}

func PublicIntro(agentType string) string {
	switch agentType {
	case Gardener:
		return "I'm a gardening services agent. Ask about services, pricing, availability, and service area."
	case Recruiter:
		return "I'm a hiring agent. Ask about open roles, the company, and the interview process."
	case Doctor:
		return "I'm a medical practice agent. Ask about specialties, services, fees, hours, and insurance — not personal medical advice."
	default:
		return "I'm a screening agent. I can answer questions about this candidate's experience and skills. Identity is hidden until they choose to reveal it."
	}
}

func PublicDisclaimer(agentType string) string {
	switch agentType {
	case Gardener:
		return "Contact details are not shared in this chat — request an intro to connect directly."
	case Recruiter:
		return "Ask about roles and process — candidate applications happen outside this chat."
	case Doctor:
		return "This is not medical advice. Do not share symptoms expecting diagnosis — ask about the practice only."
	default:
		return "Blind screening — contact details are not shared in this chat."
	}
}

func TypeTitle(agentType string) string {
	for _, t := range All {
		if t.ID == agentType {
			return t.Title
		}
	}
	if agentType == "" {
		return "Agent"
	}
	return strings.ToUpper(agentType[:1]) + agentType[1:]
}

// PrimaryUploadInfo describes the main file upload shown on the agent setup page.
func PrimaryUploadInfo(agentType string) (title, description string) {
	switch agentType {
	case Jobseeker:
		return "Resume", "Upload your résumé to build agent context (.pdf, .doc, .docx, .jpg, .png, .txt, .md)."
	case Doctor:
		return "Practice brochure / credentials", "Upload a PDF or scan of your practice info — same as a jobseeker résumé upload. Text is extracted and powers your agent."
	case Gardener:
		return "Services brochure", "Upload a PDF or document describing your services and pricing to build agent context."
	case Recruiter:
		return "Company / roles document", "Upload a PDF or document about your company and open roles to build agent context."
	default:
		return "Context document", "Upload a PDF or document to build agent context."
	}
}
