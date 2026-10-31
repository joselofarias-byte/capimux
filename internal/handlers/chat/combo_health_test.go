package chat

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"9router/proxy/internal/combohealth"
)

func TestRecordComboHealthClassification(t *testing.T) {
	combohealth.Default.Reset()
	t.Cleanup(combohealth.Default.Reset)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name    string
		ctx     context.Context
		err     error
		counted bool
	}{
		{"400 no cuenta", context.Background(), &upstreamError{StatusCode: http.StatusBadRequest}, false},
		{"404 no cuenta", context.Background(), &upstreamError{StatusCode: http.StatusNotFound}, false},
		{"cancelada no cuenta", canceled, errors.New("context canceled"), false},
		{"429 cuenta", context.Background(), &upstreamError{StatusCode: http.StatusTooManyRequests}, true},
		{"503 cuenta", context.Background(), &upstreamError{StatusCode: http.StatusServiceUnavailable}, true},
		{"transporte cuenta", context.Background(), errors.New("dial tcp: connection refused"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			combohealth.Default.Reset()
			recordComboHealth(c.ctx, "p", "m", false, c.err, 10)
			snap := combohealth.Default.Snapshot()
			if c.counted != (len(snap) == 1 && snap[0].ConsecFails == 1) {
				t.Fatalf("contado=%v snapshot=%+v", c.counted, snap)
			}
		})
	}
	combohealth.Default.Reset()
	recordComboHealth(context.Background(), "p", "m", true, nil, 120)
	if s := combohealth.Default.Snapshot(); len(s) != 1 || s[0].LatencyMs != 120 || s[0].ConsecFails != 0 {
		t.Fatalf("éxito = %+v", s)
	}
}
