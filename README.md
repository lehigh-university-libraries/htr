# htr

Handwritten Text Recognition

## Requirements

### System Dependencies

- **ImageMagick** (required for `htr create` command)
  - Used for image processing, word detection, and image manipulation
  - Install via:
    - macOS: `brew install imagemagick`
    - Ubuntu/Debian: `apt-get install imagemagick`
    - Windows: [Download from ImageMagick website](https://imagemagick.org/script/download.php)

## Install

You can install `htr` using homebrew

```
brew tap lehigh-university-libraries/homebrew https://github.com/lehigh-university-libraries/homebrew
brew install lehigh-university-libraries/homebrew/htr
```

### Download Binary

Instead of homebrew, you can download a binary for your system from [the latest release](https://github.com/lehigh-university-libraries/htr/releases/latest)

Then put the binary in a directory that is in your `$PATH`


## Usage

The HTR tool supports multiple providers for text extraction from images. Set the appropriate environment variables for your chosen provider or create them in a `.env` file.

### Supported Providers

#### OpenAI (default)
- Provider: `openai`
- Environment variable: `OPENAI_API_KEY`
- Models: `gpt-4o`, `gpt-4o-mini`, `gpt-4-vision-preview`

#### Azure OCR
- Provider: `azure`
- Environment variables: `AZURE_OCR_ENDPOINT`, `AZURE_OCR_API_KEY`
- Models: Uses Azure Computer Vision Read API 4.0

#### Google Gemini
- Provider: `gemini`
- Environment variable: `GEMINI_API_KEY`
- Models: `gemini-pro-vision`, `gemini-1.5-pro`, `gemini-1.5-flash`

#### Ollama (local)
- Provider: `ollama`
- Environment variable: `OLLAMA_URL` (optional, defaults to `http://localhost:11434`)
- Models: `llava`, `llava:13b`, `llava:34b`, `moondream`, etc.

### Eval

#### OpenAI Example
```bash
htr eval \
  --provider openai \
  --model gpt-4o \
  --prompt "Extract all text from this image" \
  --temperature 0.0 \
  --csv fixtures/images.csv \
  --dir /Volumes/2025-Lyrasis-Catalyst-Fund/ground-truth-documents
```

#### Azure OCR Example
```bash
htr eval \
  --provider azure \
  --prompt "Extract all text from this image" \
  --csv fixtures/images.csv \
  --dir /Volumes/2025-Lyrasis-Catalyst-Fund/ground-truth-documents
```

#### Gemini Example
```bash
htr eval \
  --provider gemini \
  --model gemini-pro-vision \
  --prompt "Extract all text from this image" \
  --temperature 0.0 \
  --csv fixtures/images.csv \
  --dir /Volumes/2025-Lyrasis-Catalyst-Fund/ground-truth-documents
```

#### Ollama Example
```bash
htr eval \
  --provider ollama \
  --model llava \
  --prompt "Extract all text from this image" \
  --temperature 0.0 \
  --csv fixtures/images.csv \
  --dir /Volumes/2025-Lyrasis-Catalyst-Fund/ground-truth-documents
```

### Create

Create hOCR XML files from images using custom word detection and LLM transcription:

```bash
# Create hOCR XML from an image (prints to stdout)
htr create --image path/to/image.jpg --provider ollama --model llava

# Save output to a file
htr create --image path/to/image.jpg --provider openai --model gpt-4o -o output.hocr

# Use different providers
htr create --image scan.png --provider gemini --model gemini-1.5-flash -o scan.hocr
```

**Note:** The `create` command requires ImageMagick to be installed on your system.

### Summary

View summary statistics from existing evaluation results:

```bash
# List all available evaluation files
htr summary

# View summary for a specific evaluation
htr summary eval_2025-07-24_07-44-38.yaml

# Or just use the filename without extension
htr summary eval_2025-07-24_07-44-38
```

## Testing Individual Items

You can test individual rows from your CSV to quickly evaluate a single provider:

```bash
# Test just the first row (index 0)
htr eval --provider azure --prompt "Extract all text from this image" --csv fixtures/images.csv --rows 0 --dir /path/to/images

# Test multiple specific rows
htr eval --provider gemini --model gemini-pro-vision --prompt "Extract all text from this image" --csv fixtures/images.csv --rows 0,5,10 --dir /path/to/images
```


## Updating

### Homebrew

If homebrew was used, you can simply upgrade the homebrew formulae for htr

```
brew update && brew upgrade htr
```

### Download Binary

If the binary was downloaded and added to the `$PATH` updating htr could look as follows. Requires [gh](https://cli.github.com/manual/installation) and `tar`

```
# update for your architecture
ARCH="htr_Linux_x86_64.tar.gz"
TAG=$(gh release list --exclude-pre-releases --exclude-drafts --limit 1 --repo lehigh-university-libraries/htr | awk '{print $3}')
gh release download $TAG --repo lehigh-university-libraries/htr --pattern $ARCH
tar -zxvf $ARCH
mv htr /directory/in/path/binary/was/placed
rm $ARCH
```
