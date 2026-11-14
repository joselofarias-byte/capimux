package chat

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// hostScript answers only the hosts a test registered. Any other host fails
// inside the process, so a forgotten URL cannot dial a provider.
type hostScript struct {
	mu    sync.Mutex
	hosts map[string]func(*http.Request) (*http.Response, error)
	seen  []string
}

func newHostScript() *hostScript {
	return &hostScript{hosts: map[string]func(*http.Request) (*http.Response, error){}}
}

func (s *hostScript) allow(host string, fn func(*http.Request) (*http.Response, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[host] = fn
}

func (s *hostScript) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	fn := s.hosts[r.URL.Host]
	s.seen = append(s.seen, r.URL.Host+r.URL.RequestURI())
	s.mu.Unlock()
	if fn == nil {
		return nil, fmt.Errorf("unscripted host %s", r.URL.Host)
	}
	resp, err := fn(r)
	if resp != nil && resp.Request == nil {
		resp.Request = r
	}
	return resp, err
}

func scriptedClient(s *hostScript) *http.Client {
	return &http.Client{Transport: s}
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d", status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func textResp(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const localChatCompletion = `{"id":"chatcmpl-local","object":"chat.completion","model":"deepseek-chat","choices":[{"index":0,"message":{"role":"assistant","content":"local-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`

func allowChatCompletion(s *hostScript, host string) {
	s.allow(host, func(r *http.Request) (*http.Response, error) {
		return jsonResp(http.StatusOK, localChatCompletion), nil
	})
}

type errReadCloser struct{ err error }

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error             { return nil }
