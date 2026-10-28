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

### Gemini spend controls and full-page OCR (v0.21.0)

`gemini.Options.ThinkingLevel` accepts `minimal`, `low`, `medium`, or `high`;
empty retains the model default. `MaxOutputTokens` bounds thinking and candidate
output together (zero omits it). `OmitTemperature` omits sampling natively.
`UsageInfo.OutputTokens` counts candidates and `ThoughtTokens` counts reasoning;
billed output is their sum. Without resolution fallback, `MAX_TOKENS` returns an
invalid-response error and the usage result, so truncated text cannot be saved.

`remoteocr.Client.OCR` sends the same bounded, authenticated multipart upload to
`/v1/ocr`. It returns provider/model plus ordered lines with embedded boxes and
words. All boxes have positive dimensions, nonnegative origins and probability
confidence; words must fit their line. Both lines and words count toward
`MaxBoxes`. Unicode text is preserved.

### Gemini 4

`gemini-4-argon` and `gemini-4-argon-cyber-permissive` use the same
`generateContent` client. For any `gemini-4*` model the client omits
`temperature`, because Gemini 4 ignores sampling parameters, and returns
`ErrorInvalidRequest` for `ThinkingLevel: "minimal"`, which the API rejects with
HTTP 400. Thinking levels are sent as the canonical upper-case enum names.
`MaxOutputTokens` above 1,048,576 is rejected at construction. The default
timeout is 15 minutes, because Google documents multi-minute latency on hard
Gemini 4 prompts. Text from multiple non-thought response parts is concatenated.

## Layout templates

`layout.Fill` asks a model to transcribe into a blank PageXML or hOCR layout
produced by a segmentation model. Any `providers.Client` works.

```go
client, err := gemini.NewClient(gemini.Options{APIKey: keySource, ThinkingLevel: "low"})
if err != nil {
    return err
}
result, err := layout.Fill(ctx, client, layout.Request{
    Model:      "gemini-4-argon",
    Image:      providers.Image{Data: pageJPEG, MediaType: "image/jpeg"},
    Template:   blankPageXML,   // or hOCR; Format is detected when empty
    Transcript: knownText,      // optional: align instead of transcribe
})
// result.Document is the filled template; result.Text is plain text in
// document order; result.Missing and result.GeometryChanged report drift.
```

The model's returned document is never trusted for geometry. Targets (words,
or lines without words) are matched by `id` and their text is spliced into
the original template bytes. PageXML gets `TextEquiv/Unicode` on words, lines,
and regions; line and region text is derived from the words. hOCR word or line
elements get their text content set. Targets without an `id` get a temporary
`htr-N` ID in the prompt only. Template errors wrap `layout.ErrInvalidTemplate`.
An unusable response is `ErrorInvalidResponse`, and the result still carries
`Usage` and the raw `Response`. `layout.Merge` re-runs the merge on a saved
response without calling the model.

Output size matters. The model re-emits the whole template, including every
coordinate, so output tokens scale with box count. Set Gemini `MaxOutputTokens`
high enough for dense word-level templates. The Claude CLI adapter caps output
at 4096 tokens, which is too small for most pages.

## Geometry providers and new transcription clients

`segment.Segmenter` takes encoded image bytes and returns an `ocrpage.Page`,
explicit capabilities and optional native response bytes. It has no dependency
on CLI configuration. Local `FileSegmenter` implementations accept prepared
files. `segeval` refuses geometry levels absent from a provider declaration.
See [SEGEVAL.md](SEGEVAL.md) for v2 remote multipart fields and capability JSON.

`azure.NewSegmenter(azure.LayoutOptions{...})` takes an explicit endpoint,
credential callback, injected HTTP client, timeout, polling interval and image
ceiling. It shares the existing analyze/poll path and validates operation URLs
against the submission origin before authorizing polls.

`textract.NewClient(ctx, textract.Options{API: sdkClient})` accepts an injected
SDK operation for application-controlled AWS configuration. Leaving `API` nil
selects the SDK's standard credential chain. Its default transport bounds
responses and rejects redirects; an injected API owns its own transport policy.
SDK dependencies are intentional: credential refresh and SigV4 signing are not
reimplemented. Both Azure Layout and Textract also implement `providers.Client`.

`mistral.NewClient(mistral.Options{...})` takes explicit credentials, endpoint,
injected HTTP client and image/request/response ceilings. It returns markdown
unchanged and records page usage in `UsageInfo.Pages`; figure/block geometry is
not exposed by this text client. `eval --provider mistral-ocr` defaults to
markdown normalization. The CLI adapter reads `MISTRAL_API_KEY`.

`openai.NewCompatible()` is the CLI adapter for the existing OpenAI byte client.
An exact chat-completions `--endpoint`, explicit model, and an API key from
`--api-key-env` are required. There are no model-specific presets or prompts.
The ordinary `openai.NewClient` remains the library constructor.

The local Kraken `Recognizer` returns plain text for a crop. It does not expose
native character cuts, and does not yet replace Scribe's word reconstruction.
Local command stderr/stdout is discarded and failures use redacted typed errors.
