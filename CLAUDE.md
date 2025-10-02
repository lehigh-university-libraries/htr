# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# CLAUDE.md

## 📚 Critical Documentation References
- **Go Conventions**: `./docs/GO_CONVENTIONS.md`
- **Project Architecture**: `./docs/ARCHITECTURE.md`

## Project Overview

HTR (Handwritten Text Recognition) is a Go CLI tool that provides text extraction from images using multiple AI vision providers. The tool supports evaluation of OCR/HTR performance by comparing provider outputs against ground truth transcripts.

### System Dependencies

- **ImageMagick**: Required for the `htr create` command
  - Used for image processing, word boundary detection, and image manipulation
  - Install: `brew install imagemagick` (macOS) or `apt-get install imagemagick` (Linux)

## Quick Start for Claude

When working on this project:
1. Always run `golangci-lint run` before suggesting code
2. New providers go in `pkg/providers/[name]/`
3. Tests use table-driven format (see conventions below)
4. All exported functions need documentation comments
5. Use `log/slog` for all logging with appropriate levels

## Commands

### Build and Development
```bash
# Build the binary
go build -o htr

# Run with go run
go run main.go [command] [flags]

# Format code
go fmt ./...

# Run linter (uses .golangci.yaml config)
golangci-lint run

# Tidy dependencies
go mod tidy

# Run tests
go test ./...

# Run tests with race detector
go test -race ./...
```

### Testing the CLI
```bash
# Show help
./htr --help

# Run evaluation with different providers
./htr eval --provider openai --model gpt-4o --prompt "Extract all text" --csv fixtures/images.csv --dir ./test-images

# View evaluation summaries
./htr summary

# Export results as CSV
./htr csv
```

---
