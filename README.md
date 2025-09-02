# htr

Handwritten Text Recognition

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
