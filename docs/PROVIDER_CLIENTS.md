# Provider client library

HTR exposes byte-oriented clients for applications that already own image
ingest, authorization, and configuration. The library clients do not read
environment variables or open image paths. The CLI's historical
`providers.Provider` interface remains an adapter for command-line use.

## Common contract

All model clients implement `providers.Client`:

```go
type Client interface {
    Name() string
    Extract(context.Context, providers.Request) (providers.Result, error)
}
```

`providers.Request.Image.Data` contains encoded image-file bytes, not base64.
Clients validate the model, prompt, media type, temperature, and configured
image ceiling before making a request. They encode the image only when building
the provider-specific payload.

```go
client, err := openai.NewClient(openai.Options{
    APIKey: func(context.Context) (string, error) {
        return secretStore.OpenAIKey()
    },
    Timeout:          90 * time.Second,
    MaxImageBytes:    20 << 20,
    MaxRequestBytes:  28 << 20,
    MaxResponseBytes: 2 << 20,
})
if err != nil {
    return err
}

result, err := client.Extract(ctx, providers.Request{
    Model:       "gpt-4o",
    Prompt:      "Return only the transcribed text.",
    Temperature: 0,
    Image: providers.Image{
        Data:      encodedPNG,
        MediaType: "image/png",
    },
})
```

OpenAI and Gemini accept an explicit credential callback. Gemini sends its key
in `x-goog-api-key`, never in the URL. Ollama accepts a generic
`httpclient.Authenticator`; omit it only for an intentionally unauthenticated
local service.

Each constructor accepts an optional `*http.Client` for tests and application
transport policy. HTR clones that client, preserves its transport, applies the
configured total timeout, and replaces redirect handling with a reject policy.
The caller's client is never mutated. Redirects are not followed because they
can move credentials and document bytes to a different origin.

## Safe errors

Byte-oriented provider and remote OCR operations return `*providers.Error`. The public error
contains only a category, HTTP status, and retry hint. It never contains an
endpoint, credential, response body, prompt, image name, or transcription.

```go
var providerErr *providers.Error
if errors.As(err, &providerErr) {
    switch providerErr.Kind {
    case providers.ErrorRateLimited, providers.ErrorTimeout:
        // Retry only under the application's bounded retry policy.
    case providers.ErrorAuthentication:
        // Refresh or repair the registered credential.
    }
}
```

Cancellation and deadlines retain `errors.Is` identity. Other transport and
credential causes are deliberately not unwrapped because Go HTTP errors often
include the full request URL.

## Authenticated remote OCR

`remoteocr.Client` calls the generic multipart operations `POST /v1/segment`
and `POST /v1/transcribe`. Both receive an `image` file and optional `model`
field. Upload filenames are generated from the canonical media type, so local
document names are not sent to the service.

```go
tokens, err := gcpidtoken.New(gcpidtoken.Options{
    Timeout:      5 * time.Second,
    MaxAudiences: 16,
})
if err != nil {
    return err
}

client, err := remoteocr.NewClient(remoteocr.Options{
    Endpoint: "https://registered-service.example",
    Authenticator: httpclient.BearerAuthenticator{
        Source:   tokens,
        Audience: "https://registered-service.example",
    },
    MaxImageBytes:    20 << 20,
    MaxRequestBytes:  22 << 20,
    MaxResponseBytes: 4 << 20,
    MaxBoxes:         50_000,
})
```

The Google metadata source caches tokens per exact audience, coalesces
concurrent refreshes, bounds the number of cached audiences and metadata bytes,
requires the metadata response marker, and lets waiting callers cancel
independently. Endpoint and audience selection belong in the application's
administrator-controlled provider registry; request data must never select
either value.

The segment response shape is:

```json
{
  "provider": "kraken",
  "words": [
    {"X": 10, "Y": 20, "Width": 100, "Height": 24, "Text": "word", "Confidence": 0.99}
  ]
}
```

The transcription response shape is:

```json
{"provider": "kraken", "model": "kraken-base", "text": "transcribed text"}
```

## Adding a provider

1. Define an options struct containing explicit endpoints, credentials or an
   authenticator, an injected `*http.Client`, timeout, and byte ceilings.
2. Implement `providers.Client` using `providers.ValidateRequest`,
   `httpclient.Secure`, and `httpclient.ReadAll`.
3. Map HTTP statuses with `providers.ErrorForStatus`; map request and auth
   failures with `providers.ErrorForRequest` and
   `providers.ErrorForAuthentication`.
4. Parse into typed response structures and reject missing required fields.
5. Add tests for exact payloads, Unicode, input and response bounds,
   cancellation, redirects, status classification, and error redaction.
6. Add a legacy `providers.Provider` adapter only when the CLI must expose the
   provider. Environment access belongs in that adapter, not in the core
   constructor.
