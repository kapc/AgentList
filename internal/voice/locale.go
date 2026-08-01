package voice

import (
	"strings"

	"github.com/chandresh/agentlist/internal/config"
)

type Locale struct {
	Language    string
	SayVoice    string
	SpeechModel string
	ReplyLang   string // instruction name for the LLM, e.g. "Gujarati"
}

func LocaleFromConfig(cfg config.Config) Locale {
	lang := strings.TrimSpace(cfg.VoiceLanguage)
	if lang == "" {
		lang = "en-US"
	}
	voice := strings.TrimSpace(cfg.VoiceSayVoice)
	switch strings.ToLower(lang) {
	case "gu-in", "gu", "gujarati":
		if voice == "" {
			voice = "Google.gu-IN-Wavenet-A"
		}
		return Locale{
			Language:    "gu-IN",
			SayVoice:    voice,
			SpeechModel: "deepgram_nova-3",
			ReplyLang:   "Gujarati",
		}
	default:
		if voice == "" {
			voice = "Polly.Joanna"
		}
		return Locale{
			Language:    lang,
			SayVoice:    voice,
			SpeechModel: "",
			ReplyLang:   "",
		}
	}
}

func (l Locale) Phrases() phrases {
	if l.ReplyLang == "Gujarati" {
		return phrases{
			Listening:     "હું સાંભળું છું.",
			NoInput:       "મને કંઈ સંભળાયું નહીં. આવજો.",
			DidNotCatch:   "મને સમજાયું નહીં. કૃપા કરીને ફરી કહો.",
			SorryRetry:    "માફ કરશો, કંઈક ખોટું થયું. કૃપા કરીને ફરી પ્રયાસ કરો.",
			LimitReached:  "આ વાતચીતની મર્યાદા પૂરી થઈ ગઈ છે. આવજો.",
			Unavailable:   "આ એજન્ટ હમણાં ઉપલબ્ધ નથી. કૃપા કરીને પછી પ્રયાસ કરો.",
			ListeningHint: "હું સાંભળું છું.",
			DoctorWelcome: "નમસ્તે. હું આ ક્લિનિકની માહિતી એજન્ટ છું. સેવાઓ, સમય, ફી અથવા એપોઇન્ટમેન્ટ વિશે પૂછો.",
			GardenWelcome: "નમસ્તે. હું બગીચાની સેવાઓની એજન્ટ છું. સેવાઓ અથવા મુલાકાત વિશે પૂછો.",
			BookedFmt:     "તમારી એપોઇન્ટમેન્ટ %s માટે કન્ફર્મ થઈ ગઈ છે.",
		}
	}
	return phrases{
		Listening:     "I'm listening.",
		NoInput:       "I did not hear anything. Goodbye.",
		DidNotCatch:   "I did not catch that. Please repeat your question.",
		SorryRetry:    "Sorry, something went wrong. Please try again.",
		LimitReached:  "This call has reached the conversation limit. Goodbye.",
		Unavailable:   "This agent is not available right now. Please try again later.",
		ListeningHint: "I'm listening.",
		DoctorWelcome: "",
		GardenWelcome: "",
		BookedFmt:     "Your appointment is confirmed for %s.",
	}
}

type phrases struct {
	Listening     string
	NoInput       string
	DidNotCatch   string
	SorryRetry    string
	LimitReached  string
	Unavailable   string
	ListeningHint string
	DoctorWelcome string
	GardenWelcome string
	BookedFmt     string
}
