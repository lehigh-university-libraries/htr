package gcpidtoken

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceCachesAndCoalescesPerAudience(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	token := testToken(time.Now().Add(time.Hour))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Metadata-Flavor") != "Google" || request.URL.Query().Get("format") != "full" {
			t.Error("metadata request headers or format missing")
		}
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte(token))
	}))
	defer server.Close()
	source, err := New(Options{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	const workers = 20
	var wait sync.WaitGroup
	wait.Add(workers)
	errorsSeen := make(chan error, workers)
	for range workers {
		go func() {
			defer wait.Done()
			got, tokenErr := source.Token(context.Background(), "audience-a")
			if tokenErr != nil {
				errorsSeen <- tokenErr
			} else if got != token {
				errorsSeen <- fmt.Errorf("unexpected token")
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for tokenErr := range errorsSeen {
		t.Error(tokenErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("metadata calls = %d, want 1", calls.Load())
	}
	if _, err := source.Token(context.Background(), "audience-b"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("metadata calls = %d, want 2", calls.Load())
	}
}

func TestSourceCallerCancellationIsIndependent(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte(testToken(time.Now().Add(time.Hour))))
	}))
	defer server.Close()
	source, err := New(Options{Endpoint: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, tokenErr := source.Token(ctx, "audience")
		result <- tokenErr
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	close(release)
	if _, err := source.Token(context.Background(), "audience"); err != nil {
		t.Fatalf("shared fetch did not complete: %v", err)
	}
}

func TestSourceErrorsAreRedactedAndBounded(t *testing.T) {
	t.Parallel()
	secret := "upstream-secret-body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()
	source, err := New(Options{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	audience := "private-audience"
	_, err = source.Token(context.Background(), audience)
	if !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("expected unavailable token, got %v", err)
	}
	message := err.Error()
	if strings.Contains(message, secret) || strings.Contains(message, audience) || strings.Contains(message, server.URL) {
		t.Fatalf("error leaked sensitive content: %q", message)
	}

	largeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte(strings.Repeat("x", maxMetadataBytes+1)))
	}))
	defer largeServer.Close()
	largeSource, err := New(Options{Endpoint: largeServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := largeSource.Token(context.Background(), "audience"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("expected bounded response failure, got %v", err)
	}
}

func TestSourceBlocksRedirects(t *testing.T) {
	t.Parallel()
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationCalls.Add(1)
	}))
	defer destination.Close()
	sourceServer := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusTemporaryRedirect))
	defer sourceServer.Close()
	source, err := New(Options{Endpoint: sourceServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background(), "audience"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("expected redirect failure, got %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("redirect destination received metadata request")
	}
}

func TestSourceBoundsAudienceCache(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte(testToken(time.Now().Add(time.Hour))))
	}))
	defer server.Close()
	source, err := New(Options{Endpoint: server.URL, MaxAudiences: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background(), "audience-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background(), "audience-b"); !errors.Is(err, ErrTokenUnavailable) {
		t.Fatalf("expected cache bound, got %v", err)
	}
}

func TestSourceRejectsNonMetadataAndMalformedTokenResponses(t *testing.T) {
	t.Parallel()
	for name, configure := range map[string]func(http.ResponseWriter){
		"missing marker": func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(testToken(time.Now().Add(time.Hour))))
		},
		"malformed token": func(w http.ResponseWriter) {
			w.Header().Set("Metadata-Flavor", "Google")
			_, _ = w.Write([]byte("opaque-private-token"))
		},
	} {
		configure := configure
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { configure(w) }))
			defer server.Close()
			source, err := New(Options{Endpoint: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Token(context.Background(), "audience"); !errors.Is(err, ErrTokenUnavailable) || strings.Contains(err.Error(), "opaque") {
				t.Fatalf("expected redacted token rejection, got %v", err)
			}
		})
	}
}

func testToken(expiry time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiry.Unix())))
	return "header." + payload + ".signature"
}
