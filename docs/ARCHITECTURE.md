## Architecture

### System Dependencies

- **ImageMagick**: Required for the `create` command
  - Used for image processing, word boundary detection, and image manipulation
  - Commands used: `magick identify`, `magick convert` with various image processing operations

### Core Components

**Provider System (`pkg/providers/`)**
- `interface.go`: Defines the byte-oriented `Client`, `Request`, `Result`, and
  redacted typed-error contracts used by embedding applications.
  - `UsageInfo` tracks input/output token counts from API responses.
  - Requests contain encoded image bytes and an image media type; core clients
    never open application paths.
  - The historical `Provider`/`ExtractText` contract is a CLI compatibility
    adapter.
- `registry.go`: Manages registration and retrieval of providers
- Each provider package (`openai/`, `azure/`, `claude/`, `gemini/`, `ollama/`) implements the Provider interface
  - Providers capture token usage from their respective API responses
  - Azure OCR returns empty usage info (service doesn't provide token data)
- `httpclient/`: Owns bounded response reads, exact endpoint validation,
  authentication primitives, injected-client cloning, and redirect rejection.
- `auth/gcpidtoken/`: Provides bounded, per-audience Google identity-token
  caching for registered private services.
- `remoteocr/`: Provides byte-oriented multipart segmentation and
  transcription operations for a generic remote OCR service.

See [Provider client library](PROVIDER_CLIENTS.md) for the public integration
contract and extension checklist.

**Command Structure (`cmd/`)**
- `root.go`: Root cobra command with logging configuration
- `eval.go`: Main evaluation command that orchestrates provider calls and accuracy calculations
  - Also includes `summary`, `csv`, `backfill`, and `cost` commands
- `eval-external.go`: Evaluation command for external model transcriptions
- `create.go`: Command for creating hOCR XML from images using custom word detection and LLM transcription

**Evaluation System**
- Supports CSV-based batch evaluation of images against ground truth transcripts
- Calculates character similarity, word similarity, word accuracy, and word error rate
- Tracks token usage (input/output tokens) from API responses for cost estimation
- Results saved as YAML files in `evals/` directory with token counts
- Supports testing specific rows with `--rows` flag

**Cost Estimation System**
- `cost` command analyzes token usage from evaluation results
- Calculates average tokens per document (input and output separately)
- Projects costs for large-scale transcription based on user-provided pricing
- Supports different pricing models (input vs output token pricing)

**HOCR Support (`pkg/hocr/`)**
- Models for parsing OCR response structures with bounding boxes
- Word and line detection utilities for spatial text analysis using ImageMagick
- Image preprocessing and word component extraction
- LLM-based transcription of detected word regions

### Provider Configuration

Each provider requires specific environment variables:
- **OpenAI**: `OPENAI_API_KEY`
- **Azure**: `AZURE_OCR_ENDPOINT`, `AZURE_OCR_API_KEY`
- **Claude**: `ANTHROPIC_API_KEY`
- **Gemini**: `GEMINI_API_KEY`
- **Ollama**: `OLLAMA_URL` (optional, defaults to localhost:11434)

### Key Files

- `main.go`: Entry point that loads .env and executes root command
- `go.mod`: Dependencies include cobra for CLI, fang for execution, and provider-specific clients
- `.goreleaser.yaml`: Release configuration for cross-platform builds and homebrew tap
- `fixtures/images.csv`: Example evaluation dataset format
- `evals/`: Directory containing evaluation result YAML files

### Adding New Providers

1. Create a top-level provider package implementing `providers.Client`.
2. Accept endpoint, credentials, injected HTTP client, timeout, and byte limits
   as explicit constructor options.
3. Add exact-payload, bounds, redirect, cancellation, redaction, and response
   validation tests.
4. If the CLI should expose the provider, add a legacy `providers.Provider`
   adapter and register only that adapter with the CLI registry.

Example provider structure:
```
pkg/newprovider/
├── newprovider.go       # Provider implementation
└── newprovider_test.go  # Table-driven tests
```

**Token Tracking Example:**
```go
func (c *Client) Extract(ctx context.Context, request providers.Request) (providers.Result, error) {
    // ... make API call ...

    return providers.Result{
        Text: apiResponse.Text,
        Usage: providers.UsageInfo{
            InputTokens:  apiResponse.Usage.InputTokens,
            OutputTokens: apiResponse.Usage.OutputTokens,
        },
        EffectiveModel: apiResponse.Model,
    }
}
```

### Evaluation Workflow

1. Load CSV with image paths, transcript paths, and public flags
2. For each row: read ground truth, encode image as base64, call provider API
3. Capture token usage (input/output) from API response
4. Calculate accuracy metrics using Levenshtein distance algorithms
5. Save results as YAML with configuration, per-image metrics, and token counts
6. Display summary statistics (averages across all evaluated images)

### Cost Estimation Workflow

1. Read evaluation YAML file containing token usage data
2. Calculate average input and output tokens per document
3. Apply user-provided pricing (per million tokens) for input and output
4. Compute per-document cost and project total cost for N documents
5. Display detailed breakdown of token statistics and cost estimates
