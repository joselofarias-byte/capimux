package dashboard

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// pathTransport answers usageHTTPClient from a script keyed by URL path.
// It never dials: an unscripted request fails in-process, so a fetcher cannot
// reach a real provider host while these tests run.
type pathTransport struct {
	mu    sync.Mutex
	paths map[string][]pathReply
	urls  []string
}

type pathReply struct {
	status int
	header http.Header
	body   string
	err    error
}

func (p *pathTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.urls = append(p.urls, r.Method+" "+r.URL.String())
	key := r.URL.Path
	if key == "" {
		key = "/"
	}
	q := p.paths[key]
	if len(q) == 0 {
		return nil, fmt.Errorf("unscripted %s %s", r.Method, r.URL.String())
	}
	reply := q[0]
	if len(q) > 1 {
		p.paths[key] = q[1:]
	}
	if reply.err != nil {
		return nil, reply.err
	}
	status := reply.status
	if status == 0 {
		status = http.StatusOK
	}
	hdr := reply.header
	if hdr == nil {
		hdr = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        hdr,
		Body:          io.NopCloser(strings.NewReader(reply.body)),
		ContentLength: int64(len(reply.body)),
		Request:       r,
	}, nil
}

func (p *pathTransport) saw(substr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, u := range p.urls {
		if strings.Contains(u, substr) {
			return true
		}
	}
	return false
}

func okJSON(body string) pathReply { return pathReply{status: http.StatusOK, body: body} }

func statusJSON(code int, body string) pathReply {
	return pathReply{status: code, body: body}
}

func useUsageScript(t *testing.T, paths map[string][]pathReply) *pathTransport {
	t.Helper()
	pt := &pathTransport{paths: paths}
	prev := usageHTTPClient.Transport
	usageHTTPClient.Transport = pt
	t.Cleanup(func() { usageHTTPClient.Transport = prev })
	return pt
}

func quotaRow(t *testing.T, res usageResult, name string) map[string]any {
	t.Helper()
	row, ok := res.quotas[name].(map[string]any)
	if !ok {
		t.Fatalf("quota %q missing from %+v", name, res.quotas)
	}
	return row
}

func TestUsageResultAndNumberHelpers(t *testing.T) {
	withQuotas := (usageResult{plan: "P", quotas: map[string]any{"a": 1}, extra: map[string]any{"k": "v"}}).toResponse()
	if withQuotas["plan"] != "P" || withQuotas["k"] != "v" {
		t.Fatalf("quota response = %#v", withQuotas)
	}
	bare := (usageResult{plan: "P", message: "m", bare: true}).toResponse()
	if _, ok := bare["quotas"]; ok || bare["message"] != "m" || bare["plan"] != "P" {
		t.Fatalf("bare response = %#v", bare)
	}
	plain := (usageResult{message: "m"}).toResponse()
	if _, ok := plain["quotas"]; !ok {
		t.Fatalf("non-bare message must include empty quotas: %#v", plain)
	}
	empty := (usageResult{plan: "P"}).toResponse()
	if empty["plan"] != "P" {
		t.Fatalf("empty response = %#v", empty)
	}

	if got := firstNonEmptyStr("  ", "", "ok"); got != "ok" {
		t.Errorf("firstNonEmptyStr = %q", got)
	}
	if got := firstNonEmptyStr(" ", ""); got != "" {
		t.Errorf("firstNonEmptyStr empty = %q", got)
	}
	if got := firstNonEmptyAny(nil, nil, "x"); got != "x" {
		t.Errorf("firstNonEmptyAny = %#v", got)
	}
	if firstNonEmptyAny(nil, nil) != nil {
		t.Error("firstNonEmptyAny all nil")
	}

	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(1.5), 1.5, true},
		{float64(math.NaN()), 0, false},
		{float64(math.Inf(1)), 0, false},
		{float32(2.5), 2.5, true},
		{float32(float32(math.NaN())), 0, false},
		{3, 3, true},
		{int64(4), 4, true},
		{" 5.5 ", 5.5, true},
		{"nope", 0, false},
		{" ", 0, false},
		{map[string]any{"val": "6"}, 6, true},
		{map[string]any{"other": 1}, 0, false},
		{true, 0, false},
	}
	for _, tc := range cases {
		got, ok := usageFiniteNum(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("usageFiniteNum(%#v) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if usageNum("nope", 9) != 9 {
		t.Error("usageNum fallback")
	}
	if usageNum(int64(8), 0) != 8 {
		t.Error("usageNum int64")
	}
	if _, ok := usageNumOK("nan"); ok {
		t.Error("usageNumOK nan")
	}
	if _, ok := usageNumOK(float64(math.Inf(-1))); ok {
		t.Error("usageNumOK inf")
	}
	if got, ok := usageNumOK(" 1.25 "); !ok || got != 1.25 {
		t.Errorf("usageNumOK string = %v,%v", got, ok)
	}

	if usageResetTime(true) != "" || usageResetTime(math.NaN()) != "" {
		t.Error("usageResetTime rejects non-times")
	}
	frac := usageResetTime("2026-01-02T03:04:05.5+01:00")
	if !strings.HasPrefix(frac, "2026-01-02T02:04:05") {
		t.Errorf("fractional offset = %q", frac)
	}
	if usageResetTime(strings.Repeat("9", 400)) != "" {
		t.Error("overflow numeric string")
	}
	if usageResetTimeToNil("") != nil {
		t.Error("usageResetTimeToNil empty")
	}
	if usageResetTimeToNil("2026-01-02T03:04:05Z") == nil {
		t.Error("usageResetTimeToNil value")
	}
	zeroTotal := usageQuota(-3, 0, "")
	if zeroTotal["used"] != 0.0 || zeroTotal["remainingPercentage"] != 0 || zeroTotal["resetAt"] != nil {
		t.Errorf("zero total quota = %#v", zeroTotal)
	}

	_, _, _, err := usageDo(context.Background(), "\n", "https://api.deepseek.com/user/balance", nil, nil)
	if err == nil {
		t.Fatal("invalid method must fail before any dial")
	}
	blocked := useUsageScript(t, nil)
	if _, _, err := usagePost(context.Background(), "https://api.deepseek.com/user/balance", map[string]any{"a": 1}, nil); err == nil || !blocked.saw("POST") {
		t.Fatalf("usagePost err=%v urls=%v", err, blocked.urls)
	}
}

func TestGroqDurationAndQuotaHelpers(t *testing.T) {
	ms, ok := parseGroqDurationMs("1h2m3s4ms")
	want := int64(1*3600000 + 2*60000 + 3*1000 + 4)
	if !ok || ms != want {
		t.Fatalf("parseGroqDurationMs = %d,%v want %d", ms, ok, want)
	}
	h := make(http.Header)
	if groqRateLimitQuota(h, "L", "R", "Z") != nil {
		t.Fatal("missing headers")
	}
	h.Set("L", "nope")
	h.Set("R", "1")
	if groqRateLimitQuota(h, "L", "R", "Z") != nil {
		t.Fatal("non-numeric limit")
	}
	h.Set("L", "100")
	h.Set("R", "40")
	h.Set("Z", "5s")
	q := groqRateLimitQuota(h, "L", "R", "Z")
	if q == nil || q["used"] != 60.0 || q["total"] != 100.0 {
		t.Fatalf("quota = %#v", q)
	}
	if q["resetAt"] == nil {
		t.Fatal("resetAt from duration")
	}
}

func TestFetchDeepseekUsageLocal(t *testing.T) {
	if msg := fetchDeepseekUsage(context.Background(), "  ").message; !strings.Contains(msg, "API key not available") {
		t.Fatalf("empty key: %q", msg)
	}

	t.Run("transport error", func(t *testing.T) {
		useUsageScript(t, map[string][]pathReply{
			"/user/balance": {{err: errors.New("blocked")}},
		})
		res := fetchDeepseekUsage(context.Background(), "sk-deep")
		if !strings.Contains(res.message, "blocked") {
			t.Fatalf("message %q", res.message)
		}
	})

	t.Run("auth and status", func(t *testing.T) {
		long := strings.Repeat("x", 200)
		useUsageScript(t, map[string][]pathReply{
			"/user/balance": {
				statusJSON(http.StatusUnauthorized, `{}`),
				statusJSON(http.StatusBadGateway, long),
				okJSON("not-json"),
			},
		})
		if res := fetchDeepseekUsage(context.Background(), "sk"); !strings.Contains(res.message, "authentication failed") {
			t.Fatalf("401: %q", res.message)
		}
		res := fetchDeepseekUsage(context.Background(), "sk")
		if !strings.Contains(res.message, "502") || strings.Contains(res.message, long) {
			t.Fatalf("truncated 502 message = %q", res.message)
		}
		if !strings.Contains(res.message, long[:120]) {
			t.Fatalf("502 should keep the first 120 chars: %q", res.message)
		}
		if res := fetchDeepseekUsage(context.Background(), "sk"); !strings.Contains(res.message, "not JSON") {
			t.Fatalf("non-json: %q", res.message)
		}
	})

	t.Run("balances", func(t *testing.T) {
		body := `{"is_available":false,"balance_infos":[{"currency":" usd ","total_balance":"12.5"},{"currency":"","total_balance":1},"skip",{"currency":"cny","totalBalance":3}]}`
		useUsageScript(t, map[string][]pathReply{"/user/balance": {okJSON(body), okJSON(`{"isAvailable":true,"balance_infos":[]}`), okJSON(`{"balance_infos":[{"currency":"eur","total_balance":1}]}`)}})
		res, ok := fetchProviderUsage(context.Background(), "deepseek", map[string]any{"apiKey": " sk-deep "})
		if !ok || res.plan != "DeepSeek (Insufficient Balance)" {
			t.Fatalf("plan %q ok=%v", res.plan, ok)
		}
		usd := quotaRow(t, res, "Balance (USD)")
		if usd["total"] != 12.5 || usd["currency"] != "USD" || usd["isCreditBalance"] != true {
			t.Fatalf("usd = %#v", usd)
		}
		if quotaRow(t, res, "Balance (CNY)")["total"] != 3.0 {
			t.Fatalf("cny missing: %#v", res.quotas)
		}
		res = fetchDeepseekUsage(context.Background(), "sk")
		if res.plan != "DeepSeek" || !strings.Contains(res.message, "No balance") {
			t.Fatalf("empty available: %+v", res)
		}
		res = fetchDeepseekUsage(context.Background(), "sk")
		if !strings.Contains(res.plan, "Insufficient") {
			t.Fatalf("missing availability flag: %q", res.plan)
		}
	})
}

func TestFetchGroqUsageLocal(t *testing.T) {
	if !strings.Contains(fetchGroqUsage(context.Background(), "").message, "API key not available") {
		t.Fatal("empty key")
	}
	hdr := make(http.Header)
	hdr.Set("X-Ratelimit-Limit-Requests", "100")
	hdr.Set("X-Ratelimit-Remaining-Requests", "25")
	hdr.Set("X-Ratelimit-Reset-Requests", "1m")
	hdr.Set("X-Ratelimit-Limit-Tokens", "nope")
	hdr.Set("X-Ratelimit-Remaining-Tokens", "1")
	good := make(http.Header)
	good.Set("X-Ratelimit-Limit-Requests", "10")
	good.Set("X-Ratelimit-Remaining-Requests", "4")
	good.Set("X-Ratelimit-Reset-Requests", "tomorrow")
	good.Set("X-Ratelimit-Limit-Tokens", "80")
	good.Set("X-Ratelimit-Remaining-Tokens", "20")
	good.Set("X-Ratelimit-Reset-Tokens", "2s")
	long := strings.Repeat("g", 130)
	useUsageScript(t, map[string][]pathReply{
		"/openai/v1/models": {
			{err: errors.New("blocked")},
			statusJSON(http.StatusForbidden, `{}`),
			statusJSON(http.StatusTooManyRequests, long),
			{status: http.StatusOK, header: hdr, body: `{}`},
			{status: http.StatusOK, header: good, body: `{}`},
			okJSON(`{}`),
		},
	})
	if !strings.Contains(fetchGroqUsage(context.Background(), "g").message, "blocked") {
		t.Fatal("transport")
	}
	if !strings.Contains(fetchGroqUsage(context.Background(), "g").message, "authentication failed") {
		t.Fatal("403")
	}
	msg := fetchGroqUsage(context.Background(), "g").message
	if !strings.Contains(msg, "429") || strings.Contains(msg, long) {
		t.Fatalf("truncated: %q", msg)
	}
	bad := fetchGroqUsage(context.Background(), "g")
	if _, ok := bad.quotas["Tokens"]; ok || bad.quotas["Requests"] == nil {
		t.Fatalf("bad token headers should drop tokens only: %#v", bad.quotas)
	}
	res, ok := fetchProviderUsage(context.Background(), "groq", map[string]any{"apiKey": "groq-key"})
	if !ok || res.plan != "Groq" {
		t.Fatalf("dispatch %+v ok=%v", res, ok)
	}
	if quotaRow(t, res, "Requests")["used"] != 6.0 || quotaRow(t, res, "Tokens")["used"] != 60.0 {
		t.Fatalf("quotas %#v", res.quotas)
	}
	if fetchGroqUsage(context.Background(), "g").message == "" {
		t.Fatal("no rate-limit headers")
	}
}

func TestFetchCommandCodeUsageLocal(t *testing.T) {
	if !strings.Contains(fetchCommandCodeUsage(context.Background(), " ").message, "API key not available") {
		t.Fatal("empty")
	}
	who := `/alpha/whoami`
	credits := `/alpha/billing/credits`
	subs := `/alpha/billing/subscriptions`
	fullWho := `{"org":{"id":"org 1"}}`
	fullCredits := `{"credits":{"monthlyCredits":10,"purchasedCredits":5,"freeCredits":1},"windowLimits":{"fiveHour":{"used":2,"cap":8,"resetAt":"2026-01-02T03:04:05Z"},"weekly":{"used":0,"cap":0},"ignored":"x"}}`
	fullSubs := `{"data":{"planId":"individual-pro","currentPeriodEnd":"2026-02-01T00:00:00Z"}}`

	t.Run("whoami failures", func(t *testing.T) {
		useUsageScript(t, map[string][]pathReply{
			who: {
				{err: errors.New("blocked")},
				statusJSON(http.StatusUnauthorized, `{}`),
				statusJSON(http.StatusBadGateway, `{}`),
			},
		})
		if !strings.Contains(fetchCommandCodeUsage(context.Background(), "k").message, "blocked") {
			t.Fatal("transport")
		}
		if !strings.Contains(fetchCommandCodeUsage(context.Background(), "k").message, "authentication failed") {
			t.Fatal("401")
		}
		if !strings.Contains(fetchCommandCodeUsage(context.Background(), "k").message, "502") {
			t.Fatal("502")
		}
	})

	t.Run("downstream failures", func(t *testing.T) {
		useUsageScript(t, map[string][]pathReply{
			who: {okJSON(`{}`), okJSON(`{"org":{"id":"o"}}`), okJSON(`{}`), okJSON(`{}`), okJSON(`{}`)},
			credits: {
				{err: errors.New("credits-down")},
				statusJSON(http.StatusForbidden, `{}`),
				statusJSON(http.StatusBadGateway, `{}`),
				okJSON(`{"credits":{}}`),
			},
			subs: {
				okJSON(`{}`),
				okJSON(`{}`),
				okJSON(`{}`),
				statusJSON(http.StatusBadGateway, `{}`),
			},
		})
		if !strings.Contains(fetchCommandCodeUsage(context.Background(), "k").message, "credits-down") {
			t.Fatal("credits transport")
		}
		if !strings.Contains(fetchCommandCodeUsage(context.Background(), "k").message, "authentication failed") {
			t.Fatal("credits 403")
		}
		if !strings.Contains(fetchCommandCodeUsage(context.Background(), "k").message, "credits API error (502)") {
			t.Fatal("credits 502")
		}
		if !strings.Contains(fetchCommandCodeUsage(context.Background(), "k").message, "subscriptions API error (502)") {
			t.Fatal("subs 502")
		}
	})

	t.Run("plans and windows", func(t *testing.T) {
		tr := useUsageScript(t, map[string][]pathReply{
			who:     {okJSON(fullWho), okJSON(`{}`), okJSON(`{"org":"nope"}`)},
			credits: {okJSON(fullCredits), okJSON(`{"credits":{"monthlyCredits":3}}`), okJSON(`{}`)},
			subs:    {okJSON(fullSubs), okJSON(`{"data":{"planId":"custom-plan"}}`), okJSON(`{}`)},
		})
		res, ok := fetchProviderUsage(context.Background(), "commandcode", map[string]any{"apiKey": "cc"})
		if !ok || res.plan != "Pro" {
			t.Fatalf("plan %q ok=%v", res.plan, ok)
		}
		creditsRow := quotaRow(t, res, "Credits")
		// cap 30, remaining 10+5+1 → used 14
		if creditsRow["used"] != 14.0 || creditsRow["total"] != 30.0 || creditsRow["unlimited"] != false {
			t.Fatalf("credits %#v", creditsRow)
		}
		if quotaRow(t, res, "Session (5h)")["used"] != 2.0 {
			t.Fatalf("session %#v", res.quotas["Session (5h)"])
		}
		if _, ok := res.quotas["Weekly"]; ok {
			t.Fatal("zero window must be omitted")
		}
		if !tr.saw("orgId=org+1") && !tr.saw("orgId=org%201") {
			t.Fatalf("org id not forwarded: %v", tr.urls)
		}
		unknown := fetchCommandCodeUsage(context.Background(), "k")
		if unknown.plan != "custom-plan" || quotaRow(t, unknown, "Credits")["unlimited"] != true {
			t.Fatalf("unknown plan %+v", unknown)
		}
		fallback := fetchCommandCodeUsage(context.Background(), "k")
		if fallback.plan != "Command Code" {
			t.Fatalf("default plan %q", fallback.plan)
		}
	})

	if commandCodeWindow("nope") != nil || commandCodeWindow(map[string]any{"used": 0, "cap": 0}) != nil {
		t.Fatal("empty window")
	}
	win := commandCodeWindow(map[string]any{"used": -1, "cap": 4, "resetAt": ""})
	if win["used"] != 0.0 || win["remaining"] != 4.0 || win["resetAt"] != nil {
		t.Fatalf("window %#v", win)
	}
}

func TestFetchQoderUsageLocal(t *testing.T) {
	if res := fetchQoderUsageAt(context.Background(), " ", "https://openapi.qoder.sh/api/v2/quota/usage"); !res.bare || !strings.Contains(res.message, "no access token") {
		t.Fatalf("empty %+v", res)
	}
	body := `{"expiresAt":1700000000000,"userQuota":{"total":10,"used":4,"remaining":6,"unit":"tokens"},"orgResourcePackage":{"total":"2"},"totalUsagePercentage":40,"isQuotaExceeded":true}`
	tr := useUsageScript(t, map[string][]pathReply{
		"/api/v2/quota/usage": {
			{err: errors.New("blocked")},
			okJSON("[]"),
			okJSON(body),
			okJSON(`{"userQuota":{},"orgResourcePackage":{}}`),
		},
	})
	if res := fetchQoderUsage(context.Background(), "tok"); !res.bare || !strings.Contains(res.message, "blocked") {
		t.Fatalf("transport %+v", res)
	}
	if res := fetchQoderUsageAt(context.Background(), "tok", "https://openapi.qoder.sh/api/v2/quota/usage"); !strings.Contains(res.message, "not JSON") {
		t.Fatalf("non-json %+v", res)
	}
	res, ok := fetchProviderUsage(context.Background(), "qoder", map[string]any{"accessToken": "qt"})
	if !ok {
		t.Fatal("dispatch")
	}
	user := quotaRow(t, res, "user")
	if user["used"] != 4.0 || user["unit"] != "tokens" || user["resetAt"] == "" {
		t.Fatalf("user %#v", user)
	}
	org := quotaRow(t, res, "organization")
	if org["unit"] != "credits" || org["total"] != 2.0 {
		t.Fatalf("org %#v", org)
	}
	if res.extra["isQuotaExceeded"] != true || res.extra["expiresAt"] != float64(1700000000000) {
		t.Fatalf("extra %#v", res.extra)
	}
	plain := fetchQoderUsageAt(context.Background(), "tok", "https://openapi.qoder.sh/api/v2/quota/usage")
	if plain.extra["expiresAt"] != nil || quotaRow(t, plain, "user")["resetAt"] != "" {
		t.Fatalf("no expiry %+v extra %#v", plain.quotas, plain.extra)
	}
	if !tr.saw("openapi.qoder.sh") {
		t.Fatalf("qoder host not intercepted: %v", tr.urls)
	}

	cn := useUsageScript(t, map[string][]pathReply{
		"/api/v2/quota/usage": {okJSON(`{"userQuota":{"total":1,"used":0,"remaining":1}}`)},
	})
	if _, ok := fetchProviderUsage(context.Background(), "qoder-cn", map[string]any{"apiKey": "cn"}); !ok || !cn.saw("openapi.qoder.com.cn") {
		t.Fatalf("qoder-cn urls %v", cn.urls)
	}
}

func TestCodeBuddyHelpersAndFetch(t *testing.T) {
	if codebuddyNum(" 12.5 ", 0) != 12.5 || codebuddyNum("nope", 3) != 3 || codebuddyNum("NaN", 4) != 4 || codebuddyNum("  ", 5) != 5 {
		t.Fatal("codebuddyNum")
	}
	if codebuddyCadence(map[string]any{}) != "Monthly" {
		t.Fatal("empty cadence")
	}
	daily := map[string]any{"CycleStartTime": "2026-01-01T00:00:00Z", "CycleEndTime": "2026-01-01T12:00:00Z"}
	weekly := map[string]any{"CycleStartTime": "2026-01-01T00:00:00Z", "CycleEndTime": "2026-01-08T00:00:00Z"}
	monthly := map[string]any{"CycleStartTime": "2026-01-01T00:00:00Z", "CycleEndTime": "2026-02-01T00:00:00Z"}
	if codebuddyCadence(daily) != "Daily" || codebuddyCadence(weekly) != "Weekly" || codebuddyCadence(monthly) != "Monthly" {
		t.Fatalf("cadence %s %s %s", codebuddyCadence(daily), codebuddyCadence(weekly), codebuddyCadence(monthly))
	}
	if codebuddyIsRefill(map[string]any{}) {
		t.Fatal("missing times are not a refill")
	}

	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "", " ").message, "credential not available") {
		t.Fatal("empty")
	}
	path := "/v2/billing/meter/get-user-resource"
	account := func(name, sub, start, end string, refill bool) string {
		// A bonus pack expires with its cycle. A refill's deduction end sits
		// more than two days past the cycle end.
		deduction := "1767830400000"
		if refill {
			deduction = "2049542858000"
		}
		return fmt.Sprintf(`{"PackageName":%q,"SubProductName":%q,"CycleStartTime":%q,"CycleEndTime":%q,"DeductionEndTime":%s,"CycleCapacityUsedPrecise":"3","CycleCapacitySize":9,"CapacityUsed":1,"CapacitySizePrecise":"4"}`, name, sub, start, end, deduction)
	}
	body := `{"code":0,"data":{"Response":{"Data":{"Accounts":[` +
		account("Pack", "", "2026-01-01T00:00:00Z", "2026-01-01T12:00:00Z", true) + `,` +
		account("", "", "2026-01-02T00:00:00Z", "2026-01-02T12:00:00Z", true) + `,` +
		account("", "SubOnly", "2026-01-01T00:00:00Z", "2026-01-08T00:00:00Z", false) + `,` +
		`"skip"]}}}}`
	useUsageScript(t, map[string][]pathReply{
		path: {
			{err: errors.New("blocked")},
			statusJSON(http.StatusUnauthorized, `{}`),
			statusJSON(http.StatusBadGateway, `{}`),
			okJSON("nope"),
			okJSON(`{"code":7}`),
			okJSON(`{"code":7,"msg":"nope"}`),
			okJSON(`{"code":0,"data":{}}`),
			okJSON(body),
			okJSON(`{"code":0,"data":{"Response":{"Data":{"Accounts":[` + account("", "OnlySub", "2026-01-01T00:00:00Z", "2026-03-01T00:00:00Z", true) + `]}}}}`),
		},
	})
	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "tok", "").message, "blocked") {
		t.Fatal("transport")
	}
	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "", "key").message, "invalid or expired") {
		t.Fatal("401")
	}
	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "tok", "").message, "502") {
		t.Fatal("502")
	}
	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "tok", "").message, "invalid JSON") {
		t.Fatal("json")
	}
	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "tok", "").message, "unknown") {
		t.Fatal("missing msg")
	}
	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "tok", "").message, "nope") {
		t.Fatal("msg")
	}
	if !strings.Contains(fetchCodeBuddyIntlUsage(context.Background(), "tok", "").message, "No credit package") {
		t.Fatal("empty accounts")
	}
	res, ok := fetchProviderUsage(context.Background(), "codebuddy-intl", map[string]any{"apiKey": "cb"})
	if !ok || res.plan != "Pack" {
		t.Fatalf("plan %q ok=%v msg %q", res.plan, ok, res.message)
	}
	if _, ok := res.quotas["Daily"]; !ok || res.quotas["Daily 2"] == nil {
		t.Fatalf("refill names %#v", res.quotas)
	}
	dailyRow := quotaRow(t, res, "Daily")
	if dailyRow["used"] != 3.0 || dailyRow["total"] != 9.0 || dailyRow["recurring"] != true {
		t.Fatalf("daily %#v", dailyRow)
	}
	bonus := quotaRow(t, res, "Bonus Pack 1")
	if bonus["used"] != 1.0 || bonus["total"] != 4.0 || bonus["recurring"] != false {
		t.Fatalf("bonus %#v", bonus)
	}
	subOnly := fetchCodeBuddyIntlUsage(context.Background(), "tok", "")
	if subOnly.plan != "OnlySub" || subOnly.quotas["Monthly"] == nil {
		t.Fatalf("sub fallback %+v", subOnly)
	}
}

func TestFetchKiroUsageLocal(t *testing.T) {
	if resolveKiroDefaultProfileARN("google") != kiroProfileARNSocial || resolveKiroDefaultProfileARN("github") != kiroProfileARNSocial {
		t.Fatal("social ARN")
	}
	if resolveKiroDefaultProfileARN("builder-id") != kiroProfileARNBuilder {
		t.Fatal("builder ARN")
	}
	parsed := parseKiroQuotaData(map[string]any{
		"nextDateReset": float64(1700000000),
		"usageBreakdownList": []any{
			"skip",
			map[string]any{"currentUsageWithPrecision": 2, "usageLimitWithPrecision": 10},
			map[string]any{
				"resourceType": " Agent ", "currentUsageWithPrecision": 1, "usageLimitWithPrecision": 5,
				"freeTrialInfo": map[string]any{"currentUsageWithPrecision": 1, "usageLimitWithPrecision": 2, "freeTrialExpiry": "2026-05-01T00:00:00Z"},
			},
		},
		"subscriptionInfo": map[string]any{"subscriptionTitle": "Kiro Pro"},
	})
	if parsed.plan != "Kiro Pro" || parsed.quotas["unknown"] == nil || parsed.quotas["agent"] == nil || parsed.quotas["agent_freetrial"] == nil {
		t.Fatalf("parsed %+v", parsed)
	}
	if resetAtStr("") != nil || resetAtStr("x") != "x" {
		t.Fatal("resetAtStr")
	}
	noSub := parseKiroQuotaData(map[string]any{})
	if noSub.plan != "Kiro" || len(noSub.quotas) != 0 {
		t.Fatalf("empty parse %+v", noSub)
	}

	good := `{"usageBreakdownList":[{"resourceType":"agent","currentUsageWithPrecision":1,"usageLimitWithPrecision":4}],"subscriptionInfo":{"subscriptionTitle":"Pro"}}`
	long := strings.Repeat("k", 140)

	t.Run("falls through attempts", func(t *testing.T) {
		useUsageScript(t, map[string][]pathReply{
			"/getUsageLimits": {statusJSON(http.StatusForbidden, long), okJSON(good)},
			"/":               {statusJSON(http.StatusBadGateway, "post-fail")},
		})
		res := fetchKiroUsage(context.Background(), "tok", map[string]any{"authMethod": "builder-id", "profileArn": "arn:custom"})
		if res.plan != "Pro" || quotaRow(t, res, "agent")["total"] != 4.0 {
			t.Fatalf("success on later attempt %+v", res)
		}
	})

	t.Run("auth messages", func(t *testing.T) {
		script := func() {
			useUsageScript(t, map[string][]pathReply{
				"/getUsageLimits": {statusJSON(http.StatusUnauthorized, "nope"), okJSON("not-json")},
				"/":               {{err: errors.New("post-blocked")}},
			})
		}
		script()
		idc := fetchKiroUsage(context.Background(), "tok", map[string]any{"authMethod": "idc"})
		if !strings.Contains(idc.message, "IAM Identity Center") {
			t.Fatalf("idc %q", idc.message)
		}
		script()
		social := fetchKiroUsage(context.Background(), "tok", map[string]any{"authMethod": "github"})
		if !strings.Contains(social.message, "authentication expired") {
			t.Fatalf("github %q", social.message)
		}
		script()
		other := fetchKiroUsage(context.Background(), "tok", map[string]any{"authMethod": "api_key", "profileArn": "arn:key"})
		if !strings.Contains(other.message, "rejected the current token") {
			t.Fatalf("api_key %q", other.message)
		}
	})

	t.Run("non-auth failure keeps the last error", func(t *testing.T) {
		tr := useUsageScript(t, map[string][]pathReply{
			"/getUsageLimits": {statusJSON(http.StatusBadGateway, ""), {err: errors.New("q-down")}},
			"/":               {okJSON("not-json")},
		})
		res, ok := fetchProviderUsage(context.Background(), "kiro", map[string]any{
			"accessToken": "tok",
			"providerSpecificData": map[string]any{
				"authMethod": "external_idp",
			},
		})
		if !ok || !strings.Contains(res.message, "q-get") {
			t.Fatalf("last error %q urls %v", res.message, tr.urls)
		}
		if !tr.saw(urlQueryEscapeProfile(kiroProfileARNSocial)) && !tr.saw("profileArn=") {
			t.Fatalf("expected a profile ARN on the social/default path or the explicit one: %v", tr.urls)
		}
	})

	useUsageScript(t, map[string][]pathReply{
		"/getUsageLimits": {okJSON("not-json"), okJSON("still-not")},
		"/":               {okJSON("nope")},
	})
	none := fetchKiroUsage(context.Background(), "tok", map[string]any{"authMethod": "google"})
	if !strings.Contains(none.message, "invalid JSON") {
		t.Fatalf("invalid json chain %q", none.message)
	}
}

func urlQueryEscapeProfile(arn string) string {
	return strings.ReplaceAll(arn, ":", "%3A")
}

func TestGrokCliHelpersAndFetch(t *testing.T) {
	headers := grokCliHeaders("tok", map[string]any{"email": "a@b.c", "principalId": "user-1"})
	if headers["x-email"] != "a@b.c" || headers["x-userid"] != "user-1" || headers["User-Agent"] != grokCliUserAgent {
		t.Fatalf("headers %#v", headers)
	}
	if grokCliHeaders("tok", nil)["x-email"] != "" {
		t.Fatal("nil psd")
	}
	user := map[string]any{"subscriptionTier": " super_grok "}
	cfg := map[string]any{"tier": "ignored-on-config", "subscription": map[string]any{"tier": "from-sub"}}
	if grokSubscriptionTier(user, cfg) != "super_grok" {
		t.Fatalf("user tier wins: %q", grokSubscriptionTier(user, cfg))
	}
	if grokSubscriptionTier(nil, map[string]any{"subscription_tier": "pro"}) != "pro" {
		t.Fatal("config tier")
	}
	if grokSubscriptionTier(map[string]any{}, map[string]any{"subscription": map[string]any{"tier": "from-sub"}}) != "from-sub" {
		t.Fatal("nested tier")
	}
	if grokResolvePlan(nil, nil) != "Grok Build" {
		t.Fatal("default plan")
	}
	if grokResolvePlan(map[string]any{"hasGrokCodeAccess": true}, nil) != "Grok Code" {
		t.Fatal("code access")
	}
	if grokResolvePlan(nil, map[string]any{"subscriptionTier": "x-premium"}) != "X Premium" {
		t.Fatal("title case")
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"tier":4}`))
	token := "hdr." + payload + ".sig"
	if grokPlanFromToken(token) != "X Premium Plus" {
		t.Fatalf("token plan %q", grokPlanFromToken(token))
	}
	if grokPlanFromToken("only-one") != "" || grokPlanFromToken("a."+base64.RawURLEncoding.EncodeToString([]byte("not-json"))+".b") != "" {
		t.Fatal("bad token")
	}
	unknown := base64.RawURLEncoding.EncodeToString([]byte(`{"tier":99}`))
	if grokPlanFromToken("a."+unknown+".b") != "" {
		t.Fatal("unknown tier")
	}
	badB64 := "a.###.b"
	if grokPlanFromToken(badB64) != "" {
		t.Fatal("bad b64")
	}
	if subAccessOf(map[string]any{"subscriptionTier": "free"}, nil) {
		t.Fatal("free is not paid access")
	}
	if !subAccessOf(map[string]any{"config": map[string]any{"subscriptionTier": "Pro"}}, nil) {
		t.Fatal("paid config")
	}

	billing := map[string]any{
		"monthlyLimit": 100, "includedUsed": 40,
		"onDemandCap": 0, "onDemandUsed": 5,
		"prepaidBalance":     8,
		"creditUsagePercent": 150,
		"credits":            map[string]any{"total": 20, "remaining": 5, "resetAt": "2026-04-01T00:00:00Z"},
		"billingPeriodEnd":   "2026-03-01T00:00:00Z",
		"config": map[string]any{
			"currentPeriod": map[string]any{"end": "2026-03-15T00:00:00Z"},
		},
	}
	quotas, period := parseGrokCliBilling(billing, map[string]any{})
	if period == "" || quotas["Monthly included"] == nil || quotas["On-demand"] == nil || quotas["Prepaid"] == nil || quotas["Weekly SuperGrok"] == nil || quotas["Credits"] == nil {
		t.Fatalf("parsed period %q quotas %#v", period, quotas)
	}
	weekly := quotas["Weekly SuperGrok"].(map[string]any)
	if weekly["used"] != 100.0 {
		t.Fatalf("percent clamps: %#v", weekly)
	}
	onDemand := quotas["On-demand"].(map[string]any)
	if onDemand["remainingPercentage"] != 0 || onDemand["resetAt"] == nil {
		t.Fatalf("exhausted on-demand %#v", onDemand)
	}

	usedFallback, _ := parseGrokCliBilling(map[string]any{
		"config": map[string]any{"monthly_limit": 50, "total_used": 10, "onDemandCap": 7, "onDemandUsed": -1},
	}, nil)
	if usedFallback["Monthly included"].(map[string]any)["used"] != 10.0 {
		t.Fatalf("totalUsed fallback %#v", usedFallback["Monthly included"])
	}
	if usedFallback["On-demand"].(map[string]any)["used"] != 0.0 {
		t.Fatalf("negative on-demand clamps: %#v", usedFallback["On-demand"])
	}
	remainingOnly, _ := parseGrokCliBilling(map[string]any{
		"creditBalance": map[string]any{"remaining": 0},
	}, nil)
	credits := remainingOnly["Credits"].(map[string]any)
	if credits["total"] != 1.0 || credits["remainingPercentage"] != 0.0 {
		t.Fatalf("zero remaining bag %#v", credits)
	}
	bagUsed, _ := parseGrokCliBilling(map[string]any{
		"usage": map[string]any{"limit": 9, "spent": 2},
	}, nil)
	if bagUsed["Credits"].(map[string]any)["used"] != 2.0 {
		t.Fatalf("spent bag %#v", bagUsed["Credits"])
	}
	if len(creditBags(nil, map[string]any{})) != 0 {
		t.Fatal("empty bags")
	}
	if len(nestedMap(nil, "x")) != 0 {
		t.Fatal("nil nested")
	}

	if !strings.Contains(fetchGrokCliUsage(context.Background(), " ", nil).message, "not available") {
		t.Fatal("empty token")
	}
	useUsageScript(t, map[string][]pathReply{
		"/v1/billing": {
			{err: errors.New("blocked")},
			statusJSON(http.StatusUnauthorized, `{}`),
			statusJSON(http.StatusBadGateway, strings.Repeat("z", 250)),
			okJSON("nope"),
			okJSON(`{"monthlyLimit":10,"includedUsed":1}`),
			okJSON(`{}`),
			okJSON(`{"config":{"subscriptionTier":"Pro"}}`),
		},
		"/v1/user": {
			okJSON(`{}`),
			okJSON(`{}`),
			okJSON(`{}`),
			okJSON(`{}`),
			{err: errors.New("user-down")},
			okJSON(`{"subscriptionTier":"free"}`),
			okJSON("not-json"),
		},
	})
	if !strings.Contains(fetchGrokCliUsage(context.Background(), token, nil).message, "blocked") {
		t.Fatal("transport")
	}
	if !strings.Contains(fetchGrokCliUsage(context.Background(), token, nil).message, "re-authorize") {
		t.Fatal("401")
	}
	longMsg := fetchGrokCliUsage(context.Background(), token, nil).message
	if !strings.Contains(longMsg, "502") || len(longMsg) > 240 {
		t.Fatalf("truncated billing error %q", longMsg)
	}
	if !strings.Contains(fetchGrokCliUsage(context.Background(), token, nil).message, "not JSON") {
		t.Fatal("non-json")
	}
	res, ok := fetchProviderUsage(context.Background(), "grok-cli", map[string]any{"accessToken": token})
	if !ok || res.plan != "X Premium Plus" || res.quotas["Monthly included"] == nil {
		t.Fatalf("token plan wins %+v ok=%v", res, ok)
	}
	noAllotment := fetchGrokCliUsage(context.Background(), "not-a-jwt", nil)
	if !strings.Contains(noAllotment.message, "no credit allotment") || noAllotment.plan != "Free" {
		t.Fatalf("no allotment %+v", noAllotment)
	}
	paid := fetchGrokCliUsage(context.Background(), "not-a-jwt", nil)
	if !strings.Contains(paid.message, "Subscription access") || paid.plan != "Pro" {
		t.Fatalf("paid empty %+v", paid)
	}
}

func TestFetchAntigravityDashboardLocal(t *testing.T) {
	if antigravityProjectID("  proj ") != "proj" {
		t.Fatal("string project")
	}
	if antigravityProjectID(map[string]any{"id": " from-obj "}) != "from-obj" {
		t.Fatal("object project")
	}
	if antigravityProjectID(map[string]any{"id": ""}) != "" || antigravityProjectID(3) != "" {
		t.Fatal("empty project")
	}
	weeklyRow := map[string]any{"remainingPercentage": 80.0, "total": 1000.0, "used": 200.0, "resetAt": ""}
	antigravityReconcileFamily(map[string]any{
		"gemini-3.8-flash-high":  map[string]any{"remainingPercentage": 0.0, "resetAt": "2026-06-01T00:00:00Z"},
		"gemini-3.1-flash-image": map[string]any{"remainingPercentage": 50.0},
		"other":                  "skip",
	}, "gemini-", "image", weeklyRow)
	if weeklyRow["remainingPercentage"] != 0.0 || weeklyRow["used"] != 1000.0 || weeklyRow["resetAt"] != "2026-06-01T00:00:00Z" {
		t.Fatalf("reconciled %#v", weeklyRow)
	}
	untouched := map[string]any{"remainingPercentage": 10.0, "total": 1000.0}
	antigravityReconcileFamily(map[string]any{
		"gemini-3.8-flash-high": map[string]any{"remainingPercentage": 5.0},
	}, "gemini-", "image", untouched)
	if untouched["remainingPercentage"] != 10.0 {
		t.Fatal("partial family must leave weekly alone")
	}
	antigravityReconcileFamily(map[string]any{}, "gemini-", "", weeklyRow)
	antigravityReconcileFamily(map[string]any{"gemini-x": map[string]any{"remainingPercentage": 0.0}}, "gemini-", "", "not-a-map")
	already := map[string]any{"remainingPercentage": 0.0, "total": 1000.0}
	antigravityReconcileFamily(map[string]any{
		"claude-sonnet-4-6": map[string]any{"remainingPercentage": 0.0},
	}, "claude-", "", already)
	if already["used"] != nil {
		t.Fatal("already-exhausted weekly stays")
	}

	models := `{
		"models": {
			"gemini-3.8-flash-high": {"displayName":"Flash","quotaInfo":{"remainingFraction":0,"resetTime":"2026-06-01T00:00:00Z"}},
			"claude-sonnet-4-6": {"quotaInfo":{"remainingFraction":0.5,"resetTime":"2026-06-02T00:00:00Z"}},
			"gemini-3.1-flash-image": {"quotaInfo":{"remainingFraction":0.25}},
			"not-important": {"quotaInfo":{"remainingFraction":1}},
			"hidden": {"isInternal":true,"quotaInfo":{"remainingFraction":1}},
			"broken": "nope",
			"noquota": {"displayName":"x"}
		}
	}`
	weekly := `{
		"groups": [
			{"displayName":"Gemini Pro","buckets":[
				{"window":"weekly","remainingFraction":1,"resetTime":"2026-07-01T00:00:00Z","displayName":"weekly"},
				{"window":"5h","remainingFraction":0.2,"displayName":"five hour"},
				{"window":"weekly","disabled":true,"remainingFraction":1},
				{"window":"other","remainingFraction":1},
				"skip-bucket"
			]},
			{"displayName":"Claude GPT","buckets":[
				{"window":"weekly","remainingFraction":0.4,"displayName":"weekly"},
				{"bucketId":"weekly-again","remainingFraction":0.1}
			]},
			"skip-group",
			{"displayName":"Other","buckets":[{"window":"weekly"}]}
		]
	}`
	load := `{"cloudaicompanionProject":{"id":"proj-1"},"currentTier":{"name":"Pro"},"paidTier":{"id":"standard"}}`
	tr := useUsageScript(t, map[string][]pathReply{
		"/v1internal:loadCodeAssist":           {okJSON(load)},
		"/v1internal:fetchAvailableModels":     {okJSON(models)},
		"/v1internal:retrieveUserQuotaSummary": {okJSON(weekly)},
	})
	res := fetchAntigravityDashboardUsage(context.Background(), "tok", "")
	if res.plan != "Pro" {
		t.Fatalf("plan %q", res.plan)
	}
	flash := quotaRow(t, res, "gemini-3.8-flash-high")
	if flash["used"] != 1000.0 || flash["displayName"] != "Flash" {
		t.Fatalf("flash %#v", flash)
	}
	if _, ok := res.quotas["not-important"]; ok || res.quotas["hidden"] != nil || res.quotas["broken"] != nil {
		t.Fatalf("filtered models leaked: %#v", res.quotas)
	}
	image := quotaRow(t, res, "gemini-3.1-flash-image")
	if image["displayName"] != "gemini-3.1-flash-image" || image["remainingPercentage"] != 25.0 {
		t.Fatalf("image %#v", image)
	}
	geminiWeekly := quotaRow(t, res, "gemini_weekly")
	if geminiWeekly["remainingPercentage"] != 0.0 || geminiWeekly["resetAt"] != "2026-06-01T00:00:00Z" {
		t.Fatalf("gemini weekly should be reconciled: %#v", geminiWeekly)
	}
	session := quotaRow(t, res, "gemini_session")
	if session["remainingPercentage"] != 0.0 {
		t.Fatalf("exhausted gemini models zero the session row: %#v", session)
	}
	if quotaRow(t, res, "claude_gpt_weekly")["remainingPercentage"] != 40.0 {
		t.Fatalf("claude weekly %#v", res.quotas["claude_gpt_weekly"])
	}
	if !tr.saw("daily-cloudcode-pa.googleapis.com") || !tr.saw("cloudcode-pa.googleapis.com") {
		t.Fatalf("hosts %v", tr.urls)
	}

	t.Run("free tier skips models and reads quotaSummary", func(t *testing.T) {
		useUsageScript(t, map[string][]pathReply{
			"/v1internal:loadCodeAssist":           {okJSON(`{"paidTier":{"id":"free-tier"},"currentTier":{"name":"Free"}}`)},
			"/v1internal:fetchAvailableModels":     {okJSON(models)},
			"/v1internal:retrieveUserQuotaSummary": {okJSON(`{"quotaSummary":{"groups":[{"displayName":"Gemini","buckets":[{"window":"daily","disabled":true,"displayName":"daily"}]}]}}`)},
		})
		res := fetchAntigravityDashboardUsage(context.Background(), "tok", "explicit-project")
		if res.plan != "Free" || res.quotas["gemini-3.8-flash-high"] != nil {
			t.Fatalf("free tier must not list models: %+v", res)
		}
		if res.quotas["gemini_session"] == nil {
			t.Fatalf("disabled session bucket still renders: %#v", res.quotas)
		}
	})

	t.Run("status and transport errors", func(t *testing.T) {
		useUsageScript(t, map[string][]pathReply{
			"/v1internal:loadCodeAssist": {
				{err: errors.New("load-down")},
				okJSON("not-json"),
				statusJSON(http.StatusBadGateway, `{}`),
				okJSON(`{"cloudaicompanionProject":"proj"}`),
			},
			"/v1internal:fetchAvailableModels": {
				{err: errors.New("models-down")},
				statusJSON(http.StatusForbidden, `{}`),
				statusJSON(http.StatusUnauthorized, `{}`),
				statusJSON(http.StatusBadGateway, `{}`),
			},
			"/v1internal:retrieveUserQuotaSummary": {
				{err: errors.New("weekly-down")},
			},
		})
		if !strings.Contains(fetchAntigravityDashboardUsage(context.Background(), "tok", "p").message, "models-down") {
			t.Fatal("models transport")
		}
		if !strings.Contains(fetchAntigravityDashboardUsage(context.Background(), "tok", "").message, "forbidden") {
			t.Fatal("403")
		}
		if !strings.Contains(fetchAntigravityDashboardUsage(context.Background(), "tok", "").message, "authentication expired") {
			t.Fatal("401")
		}
		if !strings.Contains(fetchAntigravityDashboardUsage(context.Background(), "tok", "").message, "502") {
			t.Fatal("502")
		}
		if fetchAntigravityDashboardWeekly(context.Background(), "tok", "p") != nil {
			t.Fatal("weekly transport")
		}
	})

	t.Run("empty weekly groups", func(t *testing.T) {
		useUsageScript(t, map[string][]pathReply{
			"/v1internal:retrieveUserQuotaSummary": {okJSON(`{"groups":[]}`), okJSON(`{}`)},
		})
		if fetchAntigravityDashboardWeekly(context.Background(), "tok", "") != nil {
			t.Fatal("empty groups")
		}
		if fetchAntigravityDashboardWeekly(context.Background(), "tok", "p") != nil {
			t.Fatal("missing groups")
		}
	})

	if _, ok := fetchProviderUsage(context.Background(), "antigravity", map[string]any{"accessToken": "tok"}); ok {
		t.Fatal("antigravity stays on its dedicated handler, not the generic switch")
	}
	if _, ok := fetchProviderUsage(context.Background(), "nope", nil); ok {
		t.Fatal("unknown provider")
	}
	res, ok := fetchProviderUsage(context.Background(), "deepseek", nil)
	if !ok || !strings.Contains(res.message, "API key not available") {
		t.Fatalf("nil data deepseek %+v", res)
	}
}

func TestAddUTCMonthsNegative(t *testing.T) {
	got := addUTCMonths(time.Date(2024, 3, 31, 10, 0, 0, 0, time.UTC), -14)
	if got.Month() != time.January || got.Year() != 2023 || got.Day() != 31 {
		t.Fatalf("addUTCMonths(-14) = %s", got)
	}
}
