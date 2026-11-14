package media

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"9router/proxy/internal/log"
)

// mediaUserinfoRe, mediaQueryRe and mediaHeaderRe keep a transport or
// parse error from carrying a base URL's userinfo, query, or a copied
// credential header into the client body or the log.
var (
	mediaUserinfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s]*@`)
	mediaQueryRe    = regexp.MustCompile(`\?[^\s]*`)
	mediaHeaderRe   = regexp.MustCompile(`(?i)(authorization|proxy-authorization|x-api-key|cookie|set-cookie)\s*[:=]\s*[^\r\n|]*`)
)

func mediaSafeText(s string) string {
	if s == "" {
		return ""
	}
	s = mediaUserinfoRe.ReplaceAllString(s, "${1}")
	s = mediaQueryRe.ReplaceAllString(s, "")
	s = mediaHeaderRe.ReplaceAllString(s, "${1}: ***")
	return log.RedactString(s)
}

// mediaSafeURL is the target logged for a media forward. An unparseable
// value is dropped: the raw string is the only place the userinfo and
// query would otherwise survive.
func mediaSafeURL(raw string) string {
	cleaned := mediaSafeText(raw)
	u, err := url.Parse(cleaned)
	if err != nil || u == nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func mediaSafeErr(err error) string {
	return mediaSafeErrDepth(err, 0)
}

func mediaSafeErrDepth(err error, depth int) string {
	if err == nil || depth > 4 {
		return ""
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		cause := ""
		if urlErr.Err != nil {
			cause = mediaSafeErrDepth(urlErr.Err, depth+1)
		}
		if urlErr.Op == "" {
			return cause
		}
		if cause == "" {
			return urlErr.Op
		}
		return urlErr.Op + ": " + cause
	}
	return mediaSafeText(err.Error())
}

// mediaPrefixBody replays the byte already read and closes the upstream body.
type mediaPrefixBody struct {
	r io.Reader
	c io.Closer
}

func (b mediaPrefixBody) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b mediaPrefixBody) Close() error {
	if b.c == nil {
		return nil
	}
	return b.c.Close()
}

// ensureMediaBody reads one byte before the handler commits a 2xx.
// A hard error with nothing the client can use must stay unwritten so the
// caller can still answer 502. EOF is an empty body, not a failure.
func ensureMediaBody(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return nil
	}
	buf := make([]byte, 1)
	n, err := resp.Body.Read(buf)
	if n == 0 && err == nil {
		n, err = resp.Body.Read(buf)
	}
	if err != nil && err != io.EOF {
		return err
	}
	orig := resp.Body
	rest := io.Reader(orig)
	if err == io.EOF {
		rest = bytes.NewReader(nil)
	}
	resp.Body = mediaPrefixBody{
		r: io.MultiReader(bytes.NewReader(buf[:n]), rest),
		c: orig,
	}
	return nil
}
