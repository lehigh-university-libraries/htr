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
1. Always run `make lint` before suggesting code
2. New providers go in `pkg/providers/[name]/`
3. Tests use table-driven format (see conventions below)
4. All exported functions need documentation comments
5. Use `log/slog` for all logging with appropriate levels

## Commands

### Makefile Commands (Preferred)
```bash
# Build the binary (includes dependency management)
make build

# Run linter (formats code, runs golangci-lint, validates renovate.json5)
make lint

# Run tests with race detector (builds first)
make test

# Install/update dependencies
make deps
```

### Testing the CLI
```bash
# Show help
./htr --help

# Run evaluation with different providers
./htr eval --provider openai --model gpt-4o --prompt "Extract all text" --csv fixtures/images.csv --dir ./test-images

# Evaluate external model transcriptions (no API calls)
./htr eval-external --csv loghi_results.csv --name loghi --dir ./fixtures

# View evaluation summaries
./htr summary

# Export results as CSV
./htr csv
```

---
