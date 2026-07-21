package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("private response stream detail")
}

func TestSecureBlocksRedirectsWithoutMutatingInjectedClient(t *testing.T) {
	t.Parallel()
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer destination.Close()
	source := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusTemporaryRedirect))
	defer source.Close()

	injected := &http.Client{Timeout: time.Minute}
	client := Secure(injected, time.Second)
	response, err := client.Get(source.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("expected blocked redirect, got %v", err)
	}
	if redirected {
		t.Fatal("redirect destination was contacted")
	}
	if injected.CheckRedirect != nil || injected.Timeout != time.Minute {
		t.Fatal("injected client was mutated")
	}
}

func TestReadAllBounded(t *testing.T) {
	t.Parallel()
	data, err := ReadAll(strings.NewReader("1234"), 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("unexpected bounded read: %q, %v", data, err)
	}
	if _, err := ReadAll(strings.NewReader("12345"), 4); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("expected size error, got %v", err)
	}
	if _, err := ReadAll(failingReader{}, 4); !errors.Is(err, ErrResponseRead) || strings.Contains(err.Error(), "private") {
		t.Fatalf("expected redacted read error, got %v", err)
	}
}

func TestParseEndpointRejectsUnsafeURLs(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{
		"ftp://example.test", "https://user:secret@example.test", "https://example.test?q=secret",
		"https://example.test#fragment", " https://example.test", "https://example.test\n.invalid",
		"https://example.test/a%2Fb",
	} {
		if _, err := ParseEndpoint(endpoint); err == nil {
			t.Errorf("expected %q to be rejected", endpoint)
		}
	}
	got, err := AppendPath("https://example.test/v1/", "/jobs")
	if err != nil || got != "https://example.test/v1/jobs" {
		t.Fatalf("AppendPath() = %q, %v", got, err)
	}
	got, err = AppendPathSegment("https://example.test/v1", "/models/", "family/model", ":run")
	if err != nil || got != "https://example.test/v1/models/family%2Fmodel:run" {
		t.Fatalf("AppendPathSegment() = %q, %v", got, err)
	}
}

func TestAuthenticationRejectsHeaderInjection(t *testing.T) {
	t.Parallel()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := StaticBearer("secret\r\nX-Evil: yes").Authorize(context.Background(), request); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expected invalid credential, got %v", err)
	}
	if err := StaticHeader("Bad Header", "secret").Authorize(context.Background(), request); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expected invalid header name, got %v", err)
	}
	if got := request.Header.Get("Authorization"); got != "" {
		t.Fatalf("unsafe header was set: %q", got)
	}
	if err := StaticBearer("secret").Authorize(context.Background(), nil); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expected nil request rejection, got %v", err)
	}
}

func TestBearerAuthenticationPreservesOnlyContextErrors(t *testing.T) {
	t.Parallel()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	authenticator := BearerAuthenticator{
		Audience: "audience",
		Source: TokenSourceFunc(func(context.Context, string) (string, error) {
			return "", context.DeadlineExceeded
		}),
	}
	if err := authenticator.Authorize(context.Background(), request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline identity, got %v", err)
	}
	authenticator.Source = TokenSourceFunc(func(context.Context, string) (string, error) {
		return "", errors.New("private upstream token error")
	})
	if err := authenticator.Authorize(context.Background(), request); !errors.Is(err, ErrInvalidCredential) || strings.Contains(err.Error(), "private") {
		t.Fatalf("expected redacted credential error, got %v", err)
	}
}
