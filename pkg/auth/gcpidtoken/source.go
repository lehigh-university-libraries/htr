// Package gcpidtoken provides cached Google metadata identity tokens.
package gcpidtoken

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lehigh-university-libraries/htr/pkg/httpclient"
)

const (
	defaultEndpoint     = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity"
	defaultTimeout      = 10 * time.Second
	defaultRefreshAhead = 2 * time.Minute
	maxTokenCacheLife   = time.Hour
	maxMetadataBytes    = 64 << 10
	maxTokenBytes       = 16 << 10
	maxAudienceBytes    = 4 << 10
	defaultMaxAudiences = 128
)

// ErrInvalidConfiguration indicates invalid source options.
var ErrInvalidConfiguration = errors.New("invalid identity token source configuration")

// ErrInvalidAudience indicates an empty or unsafe token audience.
var ErrInvalidAudience = errors.New("invalid identity token audience")

// ErrTokenUnavailable indicates a redacted metadata or token parsing failure.
var ErrTokenUnavailable = errors.New("identity token unavailable")

// Options configures an identity token Source.
type Options struct {
	HTTPClient   *http.Client
	Endpoint     string
	Timeout      time.Duration
	RefreshAhead time.Duration
	MaxAudiences int
	Now          func() time.Time
}

type cachedToken struct {
	value     string
	expiresAt time.Time
}

type tokenCall struct {
	done  chan struct{}
	token cachedToken
	err   error
}

// Source caches tokens per exact audience and coalesces concurrent metadata calls.
type Source struct {
	client       *http.Client
	endpoint     *url.URL
	timeout      time.Duration
	refreshAhead time.Duration
	maxAudiences int
	now          func() time.Time

	mu       sync.Mutex
	cache    map[string]cachedToken
	inflight map[string]*tokenCall
}

// New constructs an identity token source. It does not read environment variables.
func New(options Options) (*Source, error) {
	endpoint := options.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := httpclient.ParseEndpoint(endpoint)
	if err != nil {
		return nil, ErrInvalidConfiguration
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	refreshAhead := options.RefreshAhead
	if refreshAhead <= 0 {
		refreshAhead = defaultRefreshAhead
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	maxAudiences := options.MaxAudiences
	if maxAudiences <= 0 {
		maxAudiences = defaultMaxAudiences
	}
	return &Source{
		client:       httpclient.Secure(options.HTTPClient, timeout),
		endpoint:     parsed,
		timeout:      timeout,
		refreshAhead: refreshAhead,
		maxAudiences: maxAudiences,
		now:          now,
		cache:        make(map[string]cachedToken),
		inflight:     make(map[string]*tokenCall),
	}, nil
}

// Token returns a cached or freshly fetched token for the exact audience.
// Each caller may cancel independently; the bounded shared fetch continues for
// other callers and can populate the cache.
func (s *Source) Token(ctx context.Context, audience string) (string, error) {
	if err := validateAudience(audience); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	s.mu.Lock()
	for cachedAudience, cached := range s.cache {
		if !s.now().Add(s.refreshAhead).Before(cached.expiresAt) {
			delete(s.cache, cachedAudience)
		}
	}
	cached, cachedExists := s.cache[audience]
	if cachedExists && s.now().Add(s.refreshAhead).Before(cached.expiresAt) {
		s.mu.Unlock()
		return cached.value, nil
	}
	call, exists := s.inflight[audience]
	if !exists {
		if !cachedExists && len(s.cache)+len(s.inflight) >= s.maxAudiences {
			s.mu.Unlock()
			return "", ErrTokenUnavailable
		}
		call = &tokenCall{done: make(chan struct{})}
		s.inflight[audience] = call
		// #nosec G118 -- the shared fetch must outlive any one waiter; fetch
		// creates its own context bounded by s.timeout for all callers.
		go s.fetch(audience, call)
	}
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-call.done:
		if call.err != nil {
			return "", call.err
		}
		return call.token.value, nil
	}
}

func (s *Source) fetch(audience string, call *tokenCall) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	token, err := s.fetchToken(ctx, audience)

	s.mu.Lock()
	if err == nil {
		s.cache[audience] = token
	}
	call.token = token
	call.err = err
	delete(s.inflight, audience)
	close(call.done)
	s.mu.Unlock()
}

func (s *Source) fetchToken(ctx context.Context, audience string) (cachedToken, error) {
	endpoint := *s.endpoint
	query := endpoint.Query()
	query.Set("audience", audience)
	query.Set("format", "full")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return cachedToken{}, ErrTokenUnavailable
	}
	request.Header.Set("Metadata-Flavor", "Google")
	response, err := s.client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return cachedToken{}, context.DeadlineExceeded
		}
		return cachedToken{}, ErrTokenUnavailable
	}
	defer response.Body.Close()
	body, err := httpclient.ReadAll(response.Body, maxMetadataBytes)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return cachedToken{}, err
		}
		return cachedToken{}, ErrTokenUnavailable
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Metadata-Flavor") != "Google" {
		return cachedToken{}, ErrTokenUnavailable
	}
	token := strings.TrimSpace(string(body))
	if len(token) == 0 || len(token) > maxTokenBytes || strings.ContainsAny(token, "\r\n") {
		return cachedToken{}, ErrTokenUnavailable
	}
	expiresAt, err := tokenExpiry(token)
	if err != nil {
		return cachedToken{}, ErrTokenUnavailable
	}
	now := s.now()
	if !now.Before(expiresAt) {
		return cachedToken{}, ErrTokenUnavailable
	}
	if maximum := now.Add(maxTokenCacheLife); expiresAt.After(maximum) {
		expiresAt = maximum
	}
	return cachedToken{value: token, expiresAt: expiresAt}, nil
}

func validateAudience(audience string) error {
	if strings.TrimSpace(audience) != audience || audience == "" || len(audience) > maxAudienceBytes || strings.ContainsAny(audience, "\r\n\t") {
		return ErrInvalidAudience
	}
	return nil
}

func tokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) > maxTokenBytes {
		return time.Time{}, ErrTokenUnavailable
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > maxTokenBytes {
		return time.Time{}, ErrTokenUnavailable
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 {
		return time.Time{}, ErrTokenUnavailable
	}
	return time.Unix(claims.ExpiresAt, 0), nil
}
