// Package retrypolicy decides when a failed upstream attempt may be repeated
// on another account or combo member without risking a duplicate side effect.
//
// Chat-style generation (chat/completions, messages, responses) has no side
// effect beyond the call itself, so the router keeps failing over as before.
// Billable creations (images, videos) are non-idempotent: they rotate only
// when the failure proves the upstream did not create anything — a 4xx
// rejection, or a transport failure before the request left the box (DNS,
// connection refused, TLS handshake, proxy CONNECT). A 5xx or a failure after
// the request was sent (timeout, reset, truncated body) is ambiguous: the job
// may already exist, so the error is returned instead of retried.
//
// Inspired by the retry/recovery of 9Router Pro (MIT, © decolua and
// contributors); reimplemented in Go, no code copied.
package retrypolicy

import (
	"crypto/tls"
	"errors"
	"net"
	"syscall"
)

// NonIdempotentMedia reports whether a media endpoint creates a billable
// artifact upstream.
func NonIdempotentMedia(endpoint string) bool {
	switch endpoint {
	case "/v1/images/generations", "/images/generations",
		"/v1/images/edits", "/images/edits",
		"/v1/images/variations", "/images/variations",
		"/v1/videos/generations", "/videos/generations",
		"/v1/videos/edits", "/videos/edits",
		"/v1/videos/extensions", "/videos/extensions":
		return true
	}
	return false
}

// BeforeSend reports whether err proves the request never reached the
// upstream, so repeating it elsewhere cannot duplicate anything.
func BeforeSend(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && (opErr.Op == "dial" || opErr.Op == "proxyconnect") {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var recErr tls.RecordHeaderError
	if errors.As(err, &recErr) {
		return true
	}
	var certErr *tls.CertificateVerificationError
	return errors.As(err, &certErr)
}

// MayRotate reports whether a failed attempt may be repeated on another
// account or combo member. status is the upstream HTTP status (0 when the
// attempt failed at the transport level with err).
func MayRotate(nonIdempotent bool, status int, err error) bool {
	if !nonIdempotent {
		return true
	}
	if status > 0 {
		return status < 500
	}
	return BeforeSend(err)
}

type stopError struct{ err error }

func (e *stopError) Error() string { return e.err.Error() }
func (e *stopError) Unwrap() error { return e.err }

// Stop marks err as final: the caller must not repeat the attempt.
func Stop(err error) error {
	if err == nil {
		return nil
	}
	return &stopError{err: err}
}

// IsStop reports whether err was marked with Stop.
func IsStop(err error) bool {
	var s *stopError
	return errors.As(err, &s)
}
