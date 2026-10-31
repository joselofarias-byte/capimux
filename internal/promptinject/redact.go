package promptinject

import "regexp"

// secretPatterns cover the credential shapes Capimux handles. Redaction is a
// safety net for the admin inspector/capture views, not a content filter.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`),
	regexp.MustCompile(`(?i)("(?:api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|token|secret|password|authorization|x-api-key)"\s*:\s*")[^"]*(")`),
}

// Redact masks credential-looking substrings.
func Redact(s string) string {
	for i, re := range secretPatterns {
		if i == len(secretPatterns)-1 {
			s = re.ReplaceAllString(s, `${1}[REDACTADO]${2}`)
			continue
		}
		s = re.ReplaceAllString(s, "[REDACTADO]")
	}
	return s
}
