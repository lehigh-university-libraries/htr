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

Evaluate OCR/HTR performance by sending images to AI vision models and comparing their output against ground truth transcripts.

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
  --model mistral-small3.2:24b \
  --prompt "Extract all text from this image" \
  --temperature 0.0 \
  --csv fixtures/images.csv \
  --dir /Volumes/2025-Lyrasis-Catalyst-Fund/ground-truth-documents
```

#### Handling Unknown Characters with `--ignore`

Sometimes ground truth transcripts contain characters that cannot be deciphered. Use the `--ignore` flag to mark these unknown characters and exclude them from accuracy calculations.

**How it works:**
- Mark unknown characters in ground truth with a special pattern (e.g., `|`)
- The LLM will still transcribe the unknown character in the image as something
- HTR will automatically skip the corresponding output in the transcription when calculating metrics
- If the ignore pattern is a **standalone word** (surrounded by spaces), skip the next word in the transcription
- If the ignore pattern is **within a word**, skip the next character in the transcription

**Examples:**

```bash
# Use pipe (|) to mark unknown characters
htr eval \
  --provider openai \
  --model gpt-4o \
  --prompt "Extract all text from this image" \
  --csv fixtures/images.csv \
  --ignore '|' \
  --dir ./ground-truth

# Use multiple ignore patterns (pipe and comma)
htr eval \
  --provider gemini \
  --model gemini-1.5-flash \
  --prompt "Extract all text from this image" \
  --csv fixtures/images.csv \
  --ignore '|' \
  --ignore ',' \
  --dir ./ground-truth
```

**Ground truth examples:**

```
# Unknown word (standalone)
Ground truth: "The quick | fox"
LLM output:   "The quick brown fox"
Result:       Compares "The quick fox" vs "The quick fox" (skips "brown")

# Unknown character (within word)
Ground truth: "d|te"
LLM output:   "date"
Result:       Compares "dte" vs "dte" (skips "a")

# Multiple unknowns
Ground truth: "The | cat , jumped"
LLM output:   "The quick cat suddenly jumped"
Result:       Compares "The cat jumped" vs "The cat jumped" (skips "quick" and "suddenly")
```

**Benefits:**
- More accurate evaluation metrics when dealing with damaged or unclear documents
- Ignored characters are counted separately in results
- Character and word accuracy rates exclude unknown characters from denominators

#### Single Line Mode

**`--single-line`**: Convert multi-line documents to single-line text

Removes all newlines, carriage returns, and tabs from ground truth and transcripts, normalizing multiple spaces to single spaces. This is useful when:
- Your ground truth uses line breaks but the model output doesn't (or vice versa)
- You want to focus on content accuracy regardless of line formatting
- You need to normalize whitespace for fair comparison

```bash
# Evaluate as single-line text
htr eval \
  --provider openai \
  --model gpt-4o \
  --prompt "Extract all text from this image" \
  --csv fixtures/images.csv \
  --single-line \
  --dir ./ground-truth
```

**Examples:**

```
# With --single-line
Ground truth: "Line 1\nLine 2"
Model output: "Line 1 Line 2"
Result:       Perfect match (newlines converted to spaces)

# With tabs and multiple spaces
Ground truth: "Hello\t\tWorld\n\nTest"
Model output: "Hello World Test"
Result:       Perfect match (tabs, newlines, and multiple spaces normalized)

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

### Eval External

Evaluate transcriptions from external OCR/HTR models (like Loghi, Tesseract, Kraken, etc.) against ground truth transcripts. This command reads pre-generated transcriptions from text files and compares them to ground truth without making any API calls.

#### Usage

```bash
# Evaluate external model transcriptions
htr eval-external \
  --csv loghi_results.csv \
  --name loghi \
  --dir ./transcriptions
```

#### CSV Format

The CSV file should have 2 columns:

```csv
transcript,transcription
ground-truth-1.txt,loghi-output-1.txt
ground-truth-2.txt,loghi-output-2.txt
```

Where:
- `transcript`: Path to the ground truth transcript file
- `transcription`: Path to the external model's transcription output file

#### Example Workflow

1. Run your images through an external HTR model (e.g., Loghi):
   ```bash
   # Example: Process images with Loghi
   for img in images/*.jpg; do
     loghi-htr predict --image "$img" --output "transcriptions/$(basename $img .jpg).txt"
   done
   ```

2. Create a CSV mapping ground truth to external transcriptions:
   ```csv
   transcript,transcription
   groundtruth/page1.txt,transcriptions/page1.txt
   groundtruth/page2.txt,transcriptions/page2.txt
   ```

3. Evaluate the external model's performance:
   ```bash
   htr eval-external --csv external_model.csv --name loghi --dir ./
   ```

4. View results alongside other model evaluations:
   ```bash
   htr summary loghi
   htr csv  # Compare all models including external ones
   ```

#### Testing Specific Rows

```bash
# Test just the first few rows
htr eval-external --csv external_model.csv --name loghi --rows 0,1,2 --dir ./
```

#### Using Flags with External Models

All evaluation flags work with external model evaluations:

**Using `--ignore` for unknown characters:**

```bash
# Evaluate with unknown character handling
htr eval-external \
  --csv external_model.csv \
  --name loghi \
  --ignore '|' \
  --dir ./

# Multiple ignore patterns
htr eval-external \
  --csv tesseract_results.csv \
  --name tesseract \
  --ignore '|' \
  --ignore ',' \
  --dir ./transcriptions
```

**Using `--single-line` for normalization:**

```bash
# Convert to single-line for comparison
htr eval-external \
  --csv external_model.csv \
  --name loghi \
  --single-line \
  --dir ./

# Combine with ignore patterns
htr eval-external \
  --csv tesseract_results.csv \
  --name tesseract \
  --single-line \
  --ignore '|' \
  --dir ./transcriptions
```

These flags are useful when:
- Your ground truth contains markers for unknown/unclear characters (`--ignore`)
- External model output has different line break formatting (`--single-line`)

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
