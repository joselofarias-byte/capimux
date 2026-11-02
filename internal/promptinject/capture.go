package promptinject

import (
	"sync"
	"time"
)

// CaptureLimit is how many final requests the in-memory capture keeps.
const CaptureLimit = 20

// captureBodyMax bounds one captured body.
const captureBodyMax = 16 * 1024

// Capture is one final request as Capimux handed it to the router, after
// injection and before provider translation. It lives only in memory, only
// while the admin enabled capture, and its body is redacted.
type Capture struct {
	At        string      `json:"at"`
	Path      string      `json:"path"`
	Model     string      `json:"model"`
	APIKeyID  string      `json:"apiKeyId,omitempty"`
	Layers    []LayerInfo `json:"layers"`
	Body      string      `json:"body"`
	Truncated bool        `json:"truncated"`
}

type captureRing struct {
	mu    sync.Mutex
	items []Capture
}

var captures captureRing

func recordCapture(c Capture) {
	if len(c.Body) > captureBodyMax {
		c.Body = c.Body[:captureBodyMax]
		c.Truncated = true
	}
	c.Body = Redact(c.Body)
	c.At = time.Now().UTC().Format(time.RFC3339)
	captures.mu.Lock()
	defer captures.mu.Unlock()
	captures.items = append(captures.items, c)
	if len(captures.items) > CaptureLimit {
		captures.items = captures.items[len(captures.items)-CaptureLimit:]
	}
}

// Captures returns the captured requests, newest first.
func Captures() []Capture {
	captures.mu.Lock()
	defer captures.mu.Unlock()
	out := make([]Capture, len(captures.items))
	for i, c := range captures.items {
		out[len(out)-1-i] = c
	}
	return out
}

// ClearCaptures drops every capture (also done when capture is turned off).
func ClearCaptures() {
	captures.mu.Lock()
	captures.items = nil
	captures.mu.Unlock()
}
