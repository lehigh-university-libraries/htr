package providers

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestValidateRequestAndCanonicalMediaType(t *testing.T) {
	t.Parallel()
	request := Request{
		Model:       "model-v1",
		Prompt:      "Transcribe café 世界",
		Temperature: 0.2,
		Image:       Image{Data: []byte("image"), MediaType: `IMAGE/PNG; name="private.png"`},
	}
	if err := ValidateRequest(request, 10); err != nil {
		t.Fatal(err)
	}
	mediaType, err := CanonicalMediaType(request.Image.MediaType)
	if err != nil || mediaType != "image/png" {
		t.Fatalf("CanonicalMediaType() = %q, %v", mediaType, err)
	}

	invalid := []Request{request, request, request, request}
	invalid[0].Model = " model"
	invalid[1].Model = "model\nheader"
	invalid[2].Prompt = strings.Repeat("x", maxPromptBytes+1)
	invalid[3].Image.MediaType = "text/plain"
	for _, candidate := range invalid {
		if err := ValidateRequest(candidate, 10); errorKind(err) != ErrorInvalidRequest {
			t.Errorf("expected invalid request, got %v", err)
		}
	}
}

func TestLegacyRequestDecodesBoundedBytes(t *testing.T) {
	t.Parallel()
	config := Config{Model: "model", Prompt: "prompt"}
	encoded := base64.StdEncoding.EncodeToString([]byte("image"))
	request, err := LegacyRequest(config, "/tmp/private-name.png", encoded, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(request.Image.Data) != "image" || request.Image.MediaType != "image/png" || request.Image.Filename != "private-name.png" {
		t.Fatalf("unexpected request: %#v", request)
	}
	if _, err := LegacyRequest(config, "page.png", encoded, 4); errorKind(err) != ErrorInvalidRequest {
		t.Fatalf("expected decoded size limit, got %v", err)
	}
	if _, err := LegacyRequest(config, "page.png", "not-base64", 10); errorKind(err) != ErrorInvalidRequest {
		t.Fatalf("expected malformed base64 error, got %v", err)
	}
}

func TestErrorIsCategoricalRedactedAndContextAware(t *testing.T) {
	t.Parallel()
	errorValue := NewError(ErrorUpstream, 502, true, nil)
	if got := errorValue.Error(); got != "provider request failed: upstream (status 502)" {
		t.Fatalf("Error() = %q", got)
	}
	canceled := ErrorForRequest(context.Background(), context.Canceled)
	if !errors.Is(canceled, context.Canceled) || strings.Contains(canceled.Error(), "private") {
		t.Fatalf("cancellation identity not preserved: %v", canceled)
	}
	deadline := ErrorForAuthentication(context.Background(), context.DeadlineExceeded)
	if deadline.Kind != ErrorTimeout || !errors.Is(deadline, context.DeadlineExceeded) {
		t.Fatalf("deadline identity not preserved: %v", deadline)
	}
	privateCause := errors.New("private transport URL and token")
	redacted := NewError(ErrorTransport, 0, true, privateCause)
	if errors.Unwrap(redacted) != nil || strings.Contains(redacted.Error(), "private") {
		t.Fatalf("arbitrary cause escaped redaction: %v", redacted)
	}
}

func TestErrorForStatus(t *testing.T) {
	t.Parallel()
	tests := map[int]ErrorKind{400: ErrorInvalidRequest, 401: ErrorAuthentication, 429: ErrorRateLimited, 500: ErrorUpstream, 504: ErrorTimeout}
	for status, want := range tests {
		if got := ErrorForStatus(status).Kind; got != want {
			t.Errorf("status %d kind = %q, want %q", status, got, want)
		}
	}
}

func errorKind(err error) ErrorKind {
	var providerError *Error
	if errors.As(err, &providerError) {
		return providerError.Kind
	}
	return ""
}
