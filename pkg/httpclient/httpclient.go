// Package httpclient provides bounded, redirect-safe HTTP primitives for HTR clients.
package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrRedirectBlocked is returned when an upstream attempts to redirect a request.
var ErrRedirectBlocked = errors.New("http redirect blocked")

// ErrResponseTooLarge is returned when a response exceeds its configured limit.
var ErrResponseTooLarge = errors.New("http response exceeds configured limit")

// ErrResponseRead is returned for a redacted response stream failure.
var ErrResponseRead = errors.New("http response read failed")

// ErrInvalidCredential is returned for an empty or unsafe credential.
var ErrInvalidCredential = errors.New("invalid credential")

// TokenSource provides a bearer token for an exact audience.
type TokenSource interface {
	Token(context.Context, string) (string, error)
}

// TokenSourceFunc adapts a function into a TokenSource.
type TokenSourceFunc func(context.Context, string) (string, error)

// Token calls f.
func (f TokenSourceFunc) Token(ctx context.Context, audience string) (string, error) {
	return f(ctx, audience)
}

// Authenticator authorizes one outbound request.
type Authenticator interface {
	Authorize(context.Context, *http.Request) error
}

// AuthenticatorFunc adapts a function into an Authenticator.
type AuthenticatorFunc func(context.Context, *http.Request) error

// Authorize calls f.
func (f AuthenticatorFunc) Authorize(ctx context.Context, request *http.Request) error {
	return f(ctx, request)
}

// NoAuth leaves a request unauthenticated.
type NoAuth struct{}

// Authorize implements Authenticator.
func (NoAuth) Authorize(context.Context, *http.Request) error { return nil }

// BearerAuthenticator injects a token from Source into the Authorization header.
type BearerAuthenticator struct {
	Source   TokenSource
	Audience string
}

// Authorize implements Authenticator.
func (a BearerAuthenticator) Authorize(ctx context.Context, request *http.Request) error {
	if request == nil || a.Source == nil || strings.TrimSpace(a.Audience) == "" {
		return ErrInvalidCredential
	}
	token, err := a.Source.Token(ctx, a.Audience)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return ErrInvalidCredential
	}
	return SetHeader(request.Header, "Authorization", "Bearer "+token)
}

// StaticBearer returns an authenticator for a fixed bearer token.
func StaticBearer(token string) Authenticator {
	return AuthenticatorFunc(func(_ context.Context, request *http.Request) error {
		if request == nil {
			return ErrInvalidCredential
		}
		return SetHeader(request.Header, "Authorization", "Bearer "+token)
	})
}

// StaticHeader returns an authenticator for a fixed header value.
func StaticHeader(name, value string) Authenticator {
	return AuthenticatorFunc(func(_ context.Context, request *http.Request) error {
		if request == nil {
			return ErrInvalidCredential
		}
		return SetHeader(request.Header, name, value)
	})
}

// SetHeader validates and sets an authentication header without reflecting its value.
func SetHeader(header http.Header, name, value string) error {
	if header == nil || !validHeaderName(name) || strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || !validHeaderValue(value) {
		return ErrInvalidCredential
	}
	header.Set(name, value)
	return nil
}

// New returns an HTTP client that refuses redirects and applies a total timeout.
func New(timeout time.Duration) *http.Client {
	return Secure(nil, timeout)
}

// Secure clones an injected client and enforces redirect and timeout policy.
// The input is never mutated.
func Secure(client *http.Client, timeout time.Duration) *http.Client {
	var secured http.Client
	if client != nil {
		secured = *client
	}
	secured.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return ErrRedirectBlocked
	}
	if timeout > 0 {
		secured.Timeout = timeout
	}
	return &secured
}

// ReadAll reads at most limit bytes.
func ReadAll(reader io.Reader, limit int64) ([]byte, error) {
	if reader == nil {
		return nil, ErrResponseRead
	}
	if limit <= 0 {
		return nil, ErrResponseTooLarge
	}
	limited := io.LimitReader(reader, limit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, ErrResponseRead
	}
	if int64(len(data)) > limit {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}

// ParseEndpoint validates an exact HTTP(S) service endpoint. Credentials,
// fragments, and query strings are rejected so secrets cannot be hidden in URLs.
func ParseEndpoint(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t") {
		return nil, errors.New("invalid HTTP endpoint")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid HTTP endpoint")
	}
	return parsed, nil
}

// AppendPathSegment appends one escaped dynamic segment between fixed path text.
func AppendPathSegment(endpoint, prefix, segment, suffix string) (string, error) {
	parsed, err := ParseEndpoint(endpoint)
	if err != nil || !strings.HasPrefix(prefix, "/") || segment == "" ||
		strings.ContainsAny(prefix+suffix, "?#%\r\n") || strings.ContainsAny(segment, "\r\n\x00") {
		return "", errors.New("invalid HTTP endpoint")
	}
	rawPath := strings.TrimRight(parsed.EscapedPath(), "/") + prefix + url.PathEscape(segment) + suffix
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", errors.New("invalid HTTP endpoint")
	}
	parsed.Path = decodedPath
	parsed.RawPath = rawPath
	return parsed.String(), nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := range len(name) {
		character := name[index]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for index := range len(value) {
		if value[index] < 0x20 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

// AppendPath appends a fixed absolute path suffix to a validated service endpoint.
func AppendPath(endpoint, suffix string) (string, error) {
	parsed, err := ParseEndpoint(endpoint)
	if err != nil || !strings.HasPrefix(suffix, "/") || strings.ContainsAny(suffix, "?#\r\n") {
		return "", errors.New("invalid HTTP endpoint")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + suffix
	parsed.RawPath = ""
	return parsed.String(), nil
}
