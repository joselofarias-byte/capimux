package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const positiveCostCatalog = `{"data":[
 {"id":"paid-but-labeled-free","object":"model","pricing":{"mode":"free_tier"},"cost":{"prompt":0.5,"completion":1}},
 {"id":"some-free-paid","object":"model","cost":{"prompt":0,"completion":0.2}},
 {"id":"zero-cost","object":"model","cost":{"prompt":0,"completion":0}},
 {"id":"glm-5.3-flash","object":"model","pricing":{"mode":"free_tier"}}
]}`

func discoverPricing(t *testing.T) map[string]string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(positiveCostCatalog))
	}))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cands, err := NewOrcaRouterAdapter(ts.Client(), ts.URL+"/v1", "k").Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range cands {
		out[c.ModelID] = c.PricingMode
	}
	return out
}

func TestOrcaRouter_StrictPricing_PositiveCostIsPaid(t *testing.T) {
	t.Setenv(StrictPricingEnvVar, "1")
	got := discoverPricing(t)
	for _, id := range []string{"paid-but-labeled-free", "some-free-paid"} {
		if got[id] != "paid" {
			t.Errorf("%s: positive cost must be paid in strict mode, got %q", id, got[id])
		}
	}
	if got["zero-cost"] != "free_tier" || got["glm-5.3-flash"] != "free_tier" {
		t.Errorf("genuinely free offers must stay free_tier, got %v", got)
	}
}

// Regression: without the opt-in the classification is unchanged.
func TestOrcaRouter_StrictPricingOff_DefaultUnchanged(t *testing.T) {
	t.Setenv(StrictPricingEnvVar, "")
	got := discoverPricing(t)
	if got["paid-but-labeled-free"] != "free_tier" {
		t.Errorf("default must keep the catalog pricing mode, got %q", got["paid-but-labeled-free"])
	}
}

func TestAuditFlags_ParseValues(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "TRUE": true, " on ": true, "yes": true, "": false, "0": false, "no": false, "enabled": false} {
		t.Setenv(PreserveAccountsEnvVar, v)
		if PreserveAccountsEnabled() != want {
			t.Errorf("%q: want %v", v, want)
		}
	}
}
