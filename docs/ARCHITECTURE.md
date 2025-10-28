## Architecture

### System Dependencies

- **ImageMagick**: Required for the `create` command
  - Used for image processing, word boundary detection, and image manipulation
  - Commands used: `magick identify`, `magick convert` with various image processing operations

### Core Components

**Provider System (`pkg/providers/`)**
- `interface.go`: Defines the Provider interface that all vision providers must implement
  - `UsageInfo` struct tracks input/output token counts from API responses
  - `ExtractText` method returns text, usage info, and error
- `registry.go`: Manages registration and retrieval of providers
- Each provider package (`openai/`, `azure/`, `claude/`, `gemini/`, `ollama/`) implements the Provider interface
  - Providers capture token usage from their respective API responses
  - Azure OCR returns empty usage info (service doesn't provide token data)

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

1. Create new package in `pkg/providers/[name]/` implementing `providers.Provider` interface
2. Register provider in `cmd/eval.go` init function
3. Add provider-specific configuration validation
4. Implement `ExtractText` method with context and configuration
   - Return signature: `(string, providers.UsageInfo, error)`
   - Extract token counts from API response and populate `UsageInfo`
   - Return empty `UsageInfo{}` if provider doesn't support token tracking
5. Optionally implement `CleanResponseProvider` for custom response cleaning

Example provider structure:
```
pkg/providers/newprovider/
├── newprovider.go       # Provider implementation
└── newprovider_test.go  # Table-driven tests
```

**Token Tracking Example:**
```go
func (p *Provider) ExtractText(ctx context.Context, config providers.Config, imagePath, imageBase64 string) (string, providers.UsageInfo, error) {
    // ... make API call ...

    usage := providers.UsageInfo{
        InputTokens:  apiResponse.Usage.InputTokens,
        OutputTokens: apiResponse.Usage.OutputTokens,
    }

    return extractedText, usage, nil
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
