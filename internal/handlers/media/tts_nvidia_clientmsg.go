package media

import (
	"fmt"
	"net/url"
	"strings"

	"9router/proxy/internal/log"
)

// nvidiaTTSUpstreamClientMessage is the client text for an upstream NVIDIA TTS
// status that already stops the account loop. The status is the one
// tryNvidiaTTSConn returns. The detail keeps a short safe fragment of the
// upstream body and drops URLs' userinfo and query, API keys, and header lines.
func nvidiaTTSUpstreamClientMessage(status int, errText, requestURL, apiKey string) string {
	detail := sanitizeNvidiaTTSClientDetail(errText, requestURL, apiKey)
	if detail == "" {
		return fmt.Sprintf("nvidia tts upstream status %d", status)
	}
	return fmt.Sprintf("nvidia tts upstream status %d: %s", status, detail)
}

// sanitizeNvidiaTTSClientDetail makes an upstream body or a read error safe to
// put in a client response and in the log.
func sanitizeNvidiaTTSClientDetail(text, requestURL, apiKey string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = removeNvidiaTTSSecrets(text, requestURL, apiKey)
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = stripNvidiaTTSHeaderMaterial(line)
		if line != "" {
			kept = append(kept, line)
		}
	}
	text = redactNvidiaTTSURLs(strings.Join(kept, " "))
	text = stripNvidiaTTSSecretParams(text)
	text = removeNvidiaTTSSecrets(text, requestURL, apiKey)
	text = log.RedactString(text)
	text = strings.Join(strings.Fields(text), " ")
	const maxDetail = 240
	if len(text) > maxDetail {
		text = text[:maxDetail]
	}
	return strings.TrimSpace(text)
}

func removeNvidiaTTSSecrets(text, requestURL, apiKey string) string {
	if len(apiKey) >= 4 {
		text = strings.ReplaceAll(text, apiKey, "")
	}
	u, err := url.Parse(requestURL)
	if err != nil || u == nil || (u.User == nil && u.RawQuery == "") {
		return text
	}
	if u.User != nil {
		name := u.User.Username()
		pass, _ := u.User.Password()
		if name != "" && pass != "" {
			text = strings.ReplaceAll(text, name+":"+pass, "")
		}
		if len(pass) >= 4 {
			text = strings.ReplaceAll(text, pass, "")
		}
	}
	if len(u.RawQuery) >= 4 {
		text = strings.ReplaceAll(text, u.RawQuery, "")
	}
	return text
}

var nvidiaTTSHeaderMarkers = []string{
	"proxy-authorization:",
	"authorization:",
	"set-cookie:",
	"cookie:",
	"x-goog-api-key:",
	"x-api-key:",
	"x-api-token:",
	"api-key:",
}

func stripNvidiaTTSHeaderMaterial(line string) string {
	lower := strings.ToLower(line)
	cut := len(line)
	for _, marker := range nvidiaTTSHeaderMarkers {
		if idx := strings.Index(lower, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(line[:cut])
}

func redactNvidiaTTSURLs(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		rel := strings.Index(s[i:], "://")
		if rel < 0 {
			b.WriteString(s[i:])
			break
		}
		abs := i + rel
		if !nvidiaTTSSchemeAt(s, i, abs) {
			b.WriteString(s[i : abs+3])
			i = abs + 3
			continue
		}
		b.WriteString(s[i:abs])
		b.WriteString("://")
		i = abs + 3
		segEnd := i
		for segEnd < len(s) && !asciiSpace(s[segEnd]) {
			segEnd++
		}
		b.WriteString(stripNvidiaTTSURLSegment(s[i:segEnd]))
		i = segEnd
	}
	return b.String()
}

func nvidiaTTSSchemeAt(s string, from, colon int) bool {
	schemeStart := colon
	for schemeStart > from && isSchemeByte(s[schemeStart-1]) {
		schemeStart--
	}
	return schemeStart < colon && isAlphaByte(s[schemeStart])
}

func stripNvidiaTTSURLSegment(segment string) string {
	if at := strings.IndexByte(segment, '@'); at >= 0 {
		slash := strings.IndexByte(segment, '/')
		if slash < 0 || at < slash {
			segment = segment[at+1:]
		}
	}
	if q := strings.IndexByte(segment, '?'); q >= 0 {
		segment = segment[:q]
	}
	return segment
}

func stripNvidiaTTSSecretParams(s string) string {
	keys := []string{"api_key=", "apikey=", "api-key=", "access_token=", "token="}
	lower := strings.ToLower(s)
	for _, key := range keys {
		for {
			idx := strings.Index(lower, key)
			if idx < 0 {
				break
			}
			end := idx + len(key)
			for end < len(s) && !asciiSpace(s[end]) && s[end] != '&' {
				end++
			}
			s = s[:idx] + s[end:]
			lower = strings.ToLower(s)
		}
	}
	return s
}

func isAlphaByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isSchemeByte(c byte) bool {
	return isAlphaByte(c) || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'
}

func asciiSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
