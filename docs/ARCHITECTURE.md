## Architecture

### System Dependencies

- **ImageMagick**: Required for the `create` command
  - Used for image processing, word boundary detection, and image manipulation
  - Commands used: `magick identify`, `magick convert` with various image processing operations

### Core Components

**Provider System (`pkg/providers/`)**
- `interface.go`: Defines the Provider interface that all vision providers must implement
- `registry.go`: Manages registration and retrieval of providers
- Each provider package (`openai/`, `azure/`, `claude/`, `gemini/`, `ollama/`) implements the Provider interface

**Command Structure (`cmd/`)**
- `root.go`: Root cobra command with logging configuration
- `eval.go`: Main evaluation command that orchestrates provider calls and accuracy calculations
- `create.go`: Command for creating hOCR XML from images using custom word detection and LLM transcription

**Evaluation System**
- Supports CSV-based batch evaluation of images against ground truth transcripts
- Calculates character similarity, word similarity, word accuracy, and word error rate
- Results saved as YAML files in `evals/` directory
- Supports testing specific rows with `--rows` flag

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
5. Optionally implement `CleanResponseProvider` for custom response cleaning

Example provider structure:
```
pkg/providers/newprovider/
├── newprovider.go       # Provider implementation
└── newprovider_test.go  # Table-driven tests
```

### Evaluation Workflow

1. Load CSV with image paths, transcript paths, and public flags
2. For each row: read ground truth, encode image as base64, call provider API
3. Calculate accuracy metrics using Levenshtein distance algorithms
4. Save results as YAML with configuration and per-image metrics
5. Display summary statistics (averages across all evaluated images)
