# Segmentation evaluation

`htr segeval-external` compares exported page geometry without a provider call.
`htr segeval` runs a configured segmenter first. Both write YAML results under
`evals/segmentation/`. Provider integration decisions remain in [ROADMAP.md](ROADMAP.md).

```sh
htr segeval-external --gt fixtures/segeval/page.xml \
  --segmentation fixtures/segeval/page.kraken.json --profile letter --name example
htr summary evals/segmentation/example.yaml
htr csv --kind segmentation
htr backfill --kind segmentation
```

## Input and output

The readers detect PAGE XML, ALTO, hOCR, Kraken segmentation JSON, IIIF
AnnotationPages, and versioned htr page JSON. Plain text is accepted for
conversion, but cannot supply geometry for segmentation evaluation.

```sh
htr convert --in page.xml --to htrjson --output page.json
htr convert --in page.xml --to iiif --output annotations.json
htr convert --in kraken.json --width 2000 --height 3000 --to pagexml
htr segment --provider kraken --model blla.mlmodel page.png --to pagexml
```

Native Kraken output often omits dimensions: convert it with `--width` and
`--height` before external evaluation. ALTO physical units require `--dpi`.
Image coordinates use exclusive maximum rectangle edges. htr JSON is the
lossless interchange format; other formats have different representational
limits. Its schema is [htrpage.schema.json](../pkg/ocrpage/htrpage.schema.json).

The default reader ceilings are 64 MiB, 1,000,000 elements/points, 256 nesting
levels, and coordinates no greater than 1,000,000. Readers reject non-finite
confidences, invalid points and geometry beyond known image dimensions. ALTO
and PAGE namespace prefixes do not affect parsing.

External evaluation CSV:

```csv
ground_truth,segmentation,profile
truth/page1.xml,predicted/page1.json,letter
truth/page2.json,predicted/page2.xml,newspaper
```

Provider evaluation CSV:

```csv
image,ground_truth,profile,transcript
page1.png,truth/page1.xml,letter,truth/page1.txt
```

`--csv/-c`, `--dir`, and zero-based `--rows` select inputs. Paths may be
absolute. `--profile` defaults to `generic`; a CSV profile overrides it.
`--profile-file` supplies a custom YAML/JSON policy and takes precedence over
CSV profiles. `--level region|line|word` overrides the profile's level.
`--ignore-type` is repeatable. `--config previous.yaml` reruns saved settings.

Raw native responses and canonical pages from provider evaluations are kept in
separate files per run. YAML stores their paths, the complete profile used and
all component scores. Results are checkpointed after each row; failed rows retain
an error and do not prevent subsequent rows from running or enter score averages.
`backfill --kind segmentation` evaluates the saved canonical page against the
GT again; adding `--profile-file weights.yaml` applies revised weights only to
results with the same profile name, without calling a provider. Missing inputs
produce warnings and leave those rows unchanged. Transcription backfill is
independent. External evaluations retain references to the input files,
so keep those files available for backfill.

## Profiles and scores

Seven profiles are registered: `generic`, `printed`, `newspaper`, `manuscript`,
`letter`, `ledger`, and `form`. All weights are **uncalibrated**, and the report
records `Calibrated: false`. They are starting policies, not evidence that one
segmenter is superior. Calibration requires at least 30 marked Scribe GT pages
per profile and is deferred by the proposal.

All profiles default to line geometry. Evaluation scales the hypothesis to the
GT dimensions and uses polygons only when every retained element on both sides
has a polygon; otherwise both sides use boxes. Implicit wrapper regions are
excluded from region evaluation. A requested level that is absent is an error;
an empty detection result is allowed and counted as missing GT geometry.

Components:

- PRImA-style weighted directional merges, splits, misses, partial misses,
  false detections, and region misclassifications. Area and count scores are
  separate weighted harmonic means including reading order.
- cBAD adaptive baseline precision/recall/F for `manuscript` and `letter`.
  Every GT line must have a real baseline and at least one hypothesis line must
  have a baseline. Otherwise the report explicitly
  falls back to PRImA. Derived baselines are never accepted as baseline GT.
  A hypothesis without any baselines reports `hypothesis has no baselines`;
  partially missing hypothesis baselines still count against cBAD.
- MOTe Missing, Overlap, Trespass and Total; Excess is separate. Overlapping GT
  pixels belong to the first GT element, matching the reference library.
- Hungarian one-to-one IoU matching per overlap component, F1 at 0.5 and 0.75,
  mean F1 from 0.50 through 0.95, and mean matched IoU.
- Matched reading order, measured as normalized Kendall agreement.
- Column-span integrity for ledger and form profiles, using table cells when
  available and line-interval clustering otherwise.

Raster rows use `max(1, ceil(max(width,height)/4000))` spacing. Polygons use
row-center even-odd filling. The precision of very small shapes on large pages
is consequently limited by that row spacing. Baseline resampling follows the
reference evaluator's integer normalization at nominal five-pixel spacing,
with at least 20 points on sufficiently long lines.

The cBAD reference uses Manhattan point distance, adaptive per-GT tolerances
at 25% of the capped mean interline distance, union recall, and greedy
one-to-one precision. These details correct the proposal's original secondary
summary. [Transkribus reference](https://github.com/Transkribus/TranskribusBaseLineEvaluationScheme/tree/7a2c86abcb413f0c3be364132040eb98240238f4).

Committed synthetic reference outputs are in
[fixtures/segeval](../fixtures/segeval): four cases run against the Transkribus
v0.1.3 executable, tested within 1e-3, and three cases run against
[`mote_error`](https://github.com/THE-3TC/mote_error/tree/a74ebfac8bc3f3a13355e893a1fad5da482996d3).
Normal Go tests consume these outputs without Java or Python. Hungarian
assignment is checked against brute force through 7×7 matrices. Scribe's four
existing export goldens are copied unchanged and checked byte for byte.

## Providers and recognition

| Provider | Geometry | Configuration |
|---|---|---|
| `kraken` | line polygons and baselines | Installed `kraken`; `--model` or `KRAKEN_SEGMENTATION_MODEL` |
| `ppdoclayout` | typed regions, optional polygons | Installed `paddleocr`; `PADDLE_LAYOUT_MODEL_DIR`; CPU by default |
| `layout+kraken` | regions and lines | Both local tools; `newspapers` is an alias |
| `azure-layout` | regions, lines, words, tables | `AZURE_OCR_ENDPOINT`, `AZURE_OCR_API_KEY` |
| `textract` | regions, lines, words, tables, key/value references | AWS SDK standard credential chain and region |
| `remote` | service-declared | `HTR_REMOTE_SEGMENT_URL`; optional `HTR_REMOTE_TOKEN` |

PP-DocLayout native `polygon_points` is preserved when present; four-value
`coordinate` remains the box fallback. Unranked auxiliary regions follow ranked
regions. Layout+Kraken assigns each line to its greatest-overlap region and
keeps unmatched lines in native order at the end.

Azure uses `prebuilt-layout`, API `2024-11-30`, and the existing analyze/poll
transport. Paragraph spans own lines; line spans own words. Table cells and
figure regions are preserved. Images must be 50–10,000 pixels on each side.

Textract uses synchronous `AnalyzeDocument`, accepts PNG/JPEG images up to
10 MiB and 10,000 pixels per side, and preserves AWS implied layout order. SDK
signing and credential refresh are used instead of custom SigV4 code. `LAYOUT`
is always requested; generic/ledger add tables, form adds tables/forms/signatures,
and letter adds signatures. SDK errors are redacted. An injected SDK API owns
its transport policy; the default SDK client bounds response bytes and rejects
redirects. PDF/TIFF inputs are not exposed by this image-oriented client.

Remote v2 uses multipart `POST /v2/segment` with `image`, `model`, and JSON
`hints`, returning htr JSON. `GET /v2/capabilities?model=...` declares geometry.
Only HTTP 404/405 triggers optional v1 fallback; authentication and server errors
never trigger a second version's request. Library callers supply authenticators
and fetch capabilities during registry construction; segmentation itself has
no mutable capability cache.

`--recognizer` and `--recognizer-model` run bounded crop recognition after one
segmentation call. If the CSV includes `transcript`, YAML also records text
metrics. `pkg/pipeline.Run` supports line, word and region crops, padding,
explicit recognition configuration and cancellation. Local Kraken recognition
requires an explicitly selected recognition model. Crop recognition returns text;
`kraken.RecognizeFile` separately preserves native character cuts, word boxes,
polygons, baselines, confidence and reading order for Scribe's full-page path.

## Transcription additions

`mistral-ocr` uses `MISTRAL_API_KEY`, defaults to `mistral-ocr-latest`, preserves
markdown, and reports processed pages. `eval` defaults to markdown normalization
for this provider; `--normalize none` explicitly disables it.

```sh
htr eval --provider mistral-ocr -p 'Transcribe' -c pages.csv
htr cost mistral-ocr-latest --page-price 0.001 --doc-count 1000
htr eval --provider openai-compatible --endpoint http://localhost:8000/v1/chat/completions \
  --api-key-env OPENAI_COMPATIBLE_API_KEY -m candidate -p 'Transcribe' -c pages.csv
htr eval-external -c exported-text.csv -n candidate --normalize markdown
```

Compatible endpoints require HTTPS except for loopback hosts. `--api-key-env`
selects a credential only for the current invocation; its value is never loaded
from or saved to evaluation YAML. Supply the flag again when replaying a run
that uses a custom credential variable.

`--normalize none|markdown` is saved for evaluation reruns, CSV summaries and
backfill. Only hypothesis markup is stripped, so literal punctuation in GT is
preserved. Markdown normalization covers headings, emphasis, inline/fenced code,
list/quote prefixes, links, image syntax, HTML tags/entities and math delimiters.
It is a text transformation, not a complete Markdown renderer or sanitizer.

No model-specific presets are shipped for evaluate-first or ruled-out models.
For open-model handwriting comparisons, ROADMAP §2 records Qwen3-VL 8B as the
best open model in METATR; use the existing `ollama` provider to evaluate it.

## Scribe integration

Scribe's working tree consumes the shared crosswalks and writers, native Kraken
recognition and geometry-aware v2 segmentation. The ported crosswalk cases and
four export goldens pass unchanged. `pkg/segment/legacy` preserves its v1
box-only Kraken/newspaper behavior, including the original tests. Inferred
word boxes are an explicit legacy text-export option and are never scoring GT.

Scribe adds context/item document types, explicit immutable GT marking and ZIP
export, correction component reports, selector transforms and editable region
types. Its bundle acceptance test builds this CLI and scores `pages.csv`
without downloading images. Missing `.env` files do not prevent offline commands.

The shared module and CLI are published as v0.24.0. Scribe's integration in
[PR #148](https://github.com/lehigh-university-libraries/scribe/pull/148) pins
that version, and its ground-truth ZIP acceptance test passes against the
published dependency without a development Go workspace. Full isolated CI and
the required hosted jobs must pass before merging the Scribe integration.
Self-hosted model containers, downstream CER calibration and table
structure metrics remain out of scope as specified in the roadmap.
