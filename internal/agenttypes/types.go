package agenttypes

import (
	"fmt"
	"strings"
)

const (
	Jobseeker = "jobseeker"
	Gardener  = "gardener"
	Recruiter = "recruiter"
	Doctor    = "doctor"
)

type TypeInfo struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Icon         string `json:"icon"`
	VisitorLabel string `json:"visitor_label"`
}

var All = []TypeInfo{
	{
		ID:           Jobseeker,
		Title:        "Jobseeker",
		Description:  "Blind screening agent — share experience and skills while identity stays hidden.",
		Icon:         "💼",
		VisitorLabel: "Recruiters & hiring managers",
	},
	{
		ID:           Gardener,
		Title:        "Gardener",
		Description:  "Service agent — describe your services, pricing, and availability for local clients.",
		Icon:         "🌿",
		VisitorLabel: "Homeowners & property managers",
	},
	{
		ID:           Recruiter,
		Title:        "Recruiter",
		Description:  "Hiring agent — represent your company, open roles, and screening process to candidates.",
		Icon:         "🎯",
		VisitorLabel: "Job candidates",
	},
	{
		ID:           Doctor,
		Title:        "Doctor",
		Description:  "Practice agent — share specialties, services, fees, and availability with patients.",
		Icon:         "🩺",
		VisitorLabel: "Patients & caregivers",
	},
}

func Valid(id string) bool {
	for _, t := range All {
		if t.ID == id {
			return true
		}
	}
	return false
}

func SlugPrefix(agentType string) string {
	switch agentType {
	case Jobseeker:
		return "js"
	case Gardener:
		return "gd"
	case Recruiter:
		return "rc"
	case Doctor:
		return "dr"
	default:
		return "ag"
	}
}

func SlugForUser(userID, agentType string) string {
	s := strings.ReplaceAll(userID, "-", "")
	if len(s) > 10 {
		s = s[:10]
	}
	return fmt.Sprintf("%s-%s", SlugPrefix(agentType), s)
}

func BuildContext(agentType string, profile map[string]string, documentTexts []string) string {
	var base string
	switch agentType {
	case Gardener:
		base = fmt.Sprintf(`GARDENER PROFILE:
Business: %s
Services offered: %s
Pricing & charges: %s
Service area: %s
Availability: %s
Specialties: %s`,
			profile["business_name"], profile["services"], profile["pricing"],
			profile["service_area"], profile["availability"], profile["specialties"])
	case Recruiter:
		base = fmt.Sprintf(`RECRUITER / COMPANY PROFILE:
Company: %s
Open roles: %s
Hiring process: %s
About the company: %s
Benefits & perks: %s`,
			profile["company_name"], profile["roles_hiring"], profile["hiring_process"],
			profile["company_blurb"], profile["benefits"])
	case Doctor:
		base = fmt.Sprintf(`DOCTOR / PRACTICE PROFILE:
Practice: %s
Specialty: %s
Services offered: %s
Consultation fees: %s
Office hours: %s
Insurance accepted: %s
Languages: %s
Location / area served: %s`,
			profile["practice_name"], profile["specialty"], profile["services"],
			profile["consultation_fees"], profile["office_hours"], profile["insurance_accepted"],
			profile["languages"], profile["location_area"])
	default:
		base = profile["context"]
	}
	return appendDocuments(base, documentTexts)
}

func appendDocuments(base string, documentTexts []string) string {
	if len(documentTexts) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(base))
	b.WriteString("\n\nUPLOADED DOCUMENTS:\n")
	for i, text := range documentTexts {
		b.WriteString(fmt.Sprintf("\n--- Document %d ---\n", i+1))
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String()
}

func ProfileComplete(agentType string, profile map[string]string, hasResume bool, hasDocuments bool) bool {
	switch agentType {
	case Jobseeker:
		return hasResume || hasDocuments
	case Gardener:
		return (nonEmpty(profile["services"]) && nonEmpty(profile["pricing"])) || hasDocuments
	case Recruiter:
		return (nonEmpty(profile["company_name"]) && nonEmpty(profile["roles_hiring"])) || hasDocuments
	case Doctor:
		return (nonEmpty(profile["specialty"]) && nonEmpty(profile["services"])) || hasDocuments
	default:
		return hasDocuments
	}
}

func nonEmpty(s string) bool {
	return len(strings.TrimSpace(s)) > 0
}
