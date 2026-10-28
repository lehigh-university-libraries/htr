# Proposal: segmentation evaluation, multi-paradigm providers, and htr as the shared OCR library

Status: **implementation-ready proposal, intended to be implemented in one pass.**
Revised 2026-10-28.

How to use this document:

- Implement in the order of §13.
- Each step lists files, types, and acceptance tests.
- Provider selection follows the integration gate in [ROADMAP.md](ROADMAP.md) §1.
  Do not add clients for models in the ROADMAP's evaluate-first or ruled-out
  lists.
- Anything marked **VERIFY** is a fact gathered from secondary sources. Confirm it
  against the official docs or reference code before writing that piece, and
  correct this document if it is wrong.
- Follow `docs/GO_CONVENTIONS.md`:
  - standard library first; new dependencies are justified inline;
  - `log/slog`;
  - table-driven tests;
  - every exported identifier documented;
  - `make lint` and `make test` green in htr, and the full suite green in
    Scribe.

---

## 1. Goal and decisions

htr scores *transcription* accuracy today (`pkg/metrics`, `htr eval`,
`htr eval-external`). This work adds three things:

1. **Segmentation evaluation:** score a model's regions, lines and words
   against a ground-truth layout. The measurement is chosen by the
   ground-truth document's type.
2. **Multi-paradigm providers:** line segmenters, layout detectors, grounded
   document parsers and markdown-only VLMs. Each is a first-class provider
   with declared capabilities, and the models listed in §3 are covered.
3. **htr as the shared library:** the page model, format readers and writers,
   the IIIF crosswalk and the segmentation providers move out of Scribe.
   Scribe consumes them.

| Topic | Decision |
|---|---|
| Ground truth | From **Scribe**: human-marked ground-truth revisions of canonical IIIF AnnotationPages (§11.3) |
| Document types | Profiles `generic`, `printed`, `newspaper`, `manuscript`, `letter`, `ledger`, `form`; extensible registry |
| Default level | **Line** |
| IIIF reader | Moves from Scribe into htr behind a stable API (§6.5) |
| cBAD | Included. **The measurement is selected by the GT document's profile** (§8.4) |
| CLI | `htr segeval` / `htr segeval-external` have **the same ergonomics as `htr eval` / `eval-external`** (§10) |
| Scribe | Supports the profiles: document type on contexts and items, a GT mark and export, baseline/polygon preservation, and region types (§11) |
| New providers | Chosen **by published evidence on historical/handwritten material, not by printed-document leaderboards** ([ROADMAP.md](ROADMAP.md) §1–§2). Tier 1 in this pass: Kraken, PP-DocLayoutV3, layout+Kraken, **Azure Layout** (best traditional engine on METATR), **Mistral OCR** (best non-frontier model on METATR handwriting), AWS Textract (table/field geometry for `ledger`/`form`), a generic `openai-compatible` client, and the `remote` v2 contract. Every other model is **evaluate-first**: scored from exported outputs on Scribe GT before any client is written |
| Ruled out | olmOCR-2, RolmOCR, DeepSeek-OCR(-2), LightOnOCR-2 (95–256% handwritten CER on METATR), and Jina OCR v1 (built on DeepSeek-OCR; inferred weak). No presets, prompts or docs ship for them (ROADMAP §4) |

## 2. Prior art: evaluation methods

No Go implementation exists of any of these methods.

| Work | Measures | Our use |
|---|---|---|
| **PRImA Layout Evaluation** (Clausner, Pletschacher & Antonacopoulos, ICDAR 2011). Partial C++ source, Apache-2.0 | Merge, split, miss, partial miss, false detection and misclassification, plus reading order. Errors are weighted by **evaluation profiles**, giving area- and count-based success rates that are combined by a weighted harmonic mean | The core score. Its profiles become our document-type profiles |
| **READ-BAD / cBAD** (Grüning et al., arXiv:1705.03311; Transkribus evaluation scheme) | Baseline polylines compared with a distance tolerance, giving line-coverage R, P and F | Primary for handwritten profiles when the GT has baselines |
| **MOTe** (Bourne, Simbeye & Govia, arXiv:2603.12718 v4, 2026-10-22; `pip install mote_error`) | Mask-based Missing + Overlap + Trespass, with Excess reported separately; many-to-one assignment | Supporting score for every profile. Robust to granularity mismatch, and tracks CER better than F1 (\|ρ\| 0.82 vs 0.54) |
| **COCO IoU matching** (DocLayNet, DocLayout-YOLO) | One-to-one matching, P/R/F1 at IoU thresholds | Supporting score. No AP, because segmenters such as Kraken give no confidences |
| ICDAR 2013 HW segmentation contest; DetEval (Wolf & Jolion 2006) | One-to-many and many-to-one match counting | Covered by the PRImA split and merge classes |
| GriTS (Smock et al. 2023); TEDS (Zhong et al. 2020) | Table structure | Out of scope for v1 (§14). `ocrpage` keeps table cells so they can be added later |
| Kraken `ketos segtest`; OCR-D `ocrd-segment-evaluate` (early stage) | Pixel accuracy and mIoU | Confirms the gap |

## 3. Model landscape and paradigms

### 3.1 Paradigms

| # | Paradigm | Produces | Examples | Evaluable with |
|---|---|---|---|---|
| P1 | **Line/baseline segmenter** | Lines with polygons and baselines, sometimes typed regions; no text | Kraken BLLA, Loghi/laypa, Surya line detection | `segeval` at line level, plus cBAD |
| P2 | **Layout detector** | Typed regions (multi-point boxes) in reading order; no text | **PP-DocLayoutV3**, DocLayout-YOLO, Docling Heron | `segeval` at region level |
| P3 | **Grounded document parser** (end-to-end) | Geometry **and** text, at word/line level or only at block level | **AWS Textract**, Azure Document Intelligence layout, Google Document AI OCR, Transkribus, Surya 2, PaddleOCR-VL-1.6, dots.mocr, Chandra OCR 2, MinerU2.5 | `segeval` at the finest level it returns, and `metrics` on its text |
| P4 | **Text-only (markdown) VLM** | Text or markdown, no geometry | **Jina OCR v1**, olmOCR-2, LightOnOCR-2, DeepSeek-OCR-2, HunyuanOCR-1.5, GLM-OCR, general LLMs (current htr providers) | `metrics` only, after markdown normalization (§9.6) |
| P5 | **Line recognizer** | Text for a line crop | Kraken recognition (CATMuS, McCATMuS), TrOCR, PyLaia, PP-OCRv6 historical | `metrics`, when composed with P1/P2/P3 in a pipeline (§7.4) |

Two rules follow from this split:

- **A provider declares which levels it has geometry for.** Evaluating a level
  it lacks is an error, never a silent zero. Block-level parsers such as
  PaddleOCR-VL, dots.mocr and Chandra can only be scored at region level.
- **Any tool that exports PAGE XML, ALTO or hOCR is supported without provider
  code** through `htr segeval-external`. That includes Loghi, eScriptorium,
  Transkribus exports, Tesseract and OCR-D.

### 3.2 Providers to add or formalize

Columns: **G** is the levels with geometry (R = region, L = line, W = word).

- **Tier 1** ships in this pass.
- **Evaluate-first** models get **no client code in this pass**. They are run
  by hand on Scribe GT and scored with `segeval-external` / `eval-external`.
  They are promoted only if they pass the gate in [ROADMAP.md](ROADMAP.md) §1.
  Their rows are kept here so that the contract (§7) is designed to fit them.

The tiers follow published evidence on historical and handwritten material
(ROADMAP §2): METATR, not OmniDocBench or olmOCR-Bench. htr only ships *client
code*: model licenses bind whoever runs the model, so htr records the license
in each provider's doc comment.

| Provider (htr name) | Paradigm | G | Text | Hosting | License / terms | Why | Tier |
|---|---|---|---|---|---|---|---|
| `kraken` (moved from Scribe) | P1 (+P5 recognition) | L (polygon + baseline) | recognition separate | local subprocess | Apache-2.0 code; model-specific | Current Scribe default | 1 |
| `ppdoclayout` (PP-DocLayoutV3) | P2 | R (multi-point boxes, reading order) | no | local subprocess (`paddleocr`) or remote | Apache-2.0 | Already used inside Scribe's newspaper provider; promoted to a first-class layout segmenter | 1 |
| `layout+kraken` (formalizes Scribe "newspapers") | P1+P2 | R + L | no | local | as above | Region-ordered Kraken lines for multi-column pages | 1 |
| `azure-layout` (Document Intelligence `prebuilt-layout`) | P3 | R (paragraph roles), L, W (polygons), tables | yes | Azure API | Commercial API | **Best traditional engine on METATR** (49.1% handwritten / 30.8% printed CER) and best cloud OCR on IAM. htr already calls this endpoint for text; now return its geometry. **Highest-priority new provider** | 1 |
| `mistral-ocr` (Mistral OCR API) | P4 (markdown; figure boxes only) | none for text | yes (markdown) | Mistral API | Commercial API | 43.6% handwritten CER on METATR: best below Gemini/Claude, ahead of GPT-5.1, Azure and every open model. Not reachable today | 1 |
| `textract` (AWS Textract AnalyzeDocument) | P3 | R (LAYOUT_*), L, W (polygons), tables, key-values | yes (printed + handwriting flag) | AWS API | Commercial API | **Kept for table/field geometry for the `ledger` and `form` profiles**, not handwriting accuracy: there is no historical benchmark, and it trailed Azure on IAM. Its transcription is scored like any other provider | 1 |
| `openai-compatible` (generic) | P4 | none | yes | any OpenAI-compatible endpoint (vLLM, hosted) | per model | Generic access for evaluate-first candidates (e.g. Churro via vLLM). **No presets, prompts or docs for ruled-out models** (ROADMAP §4) | 1 |
| `remote` v2 (`/v2/segment` contract, §7.5) | any | as declared | as declared | self-hosted container | per model | The extension point for Python models (Surya, Laypa/Loghi, DocLayout-YOLO) without new Go code | 1 (contract) |
| `ollama` (existing) with Qwen3-VL 8B | P4 | none | yes | self-hosted (Scribe already runs Ollama) | Apache-2.0 | Best open model on METATR (54.9% handwritten). **No new code**: document it as a recommended model | docs only |
| Transkribus, Google Document AI, PaddleOCR-VL-1.6, dots.mocr, Chandra OCR 2, Surya OCR 2, Churro, PeroOCR, Laypa/Loghi, DocLayout-YOLO, Docling Heron | P1–P4 | varies | varies | varies | varies | No independent evidence on historical handwriting. See ROADMAP §3 for each one's evaluation path | **evaluate-first** |

Considered and **not** added as providers:

- **Ruled out by METATR:** olmOCR-2, RolmOCR, DeepSeek-OCR(-2), LightOnOCR-2;
  Jina OCR v1 by inference (ROADMAP §4).
- MinerU2.5-Pro: PDF-pipeline oriented.
- TrOCR, PyLaia, PP-OCRv6: P5 via `remote` `/v1/transcribe`, evaluate-first.
- Grounded prompting of general VLMs (asking Gemini or Qwen3-VL for boxes):
  geometry is unreliable. `layout.Fill`, which fills a segmenter's template,
  already covers the useful case.

**Segmentation has no head-to-head evidence at all** (ROADMAP §2.4). Kraken and
PP-DocLayoutV3 are tier 1 because Scribe already runs them, not because they
were shown to be best. Choosing segmenters is what `segeval` on Scribe GT is
for.

Model notes, as published by the vendors. Benchmark numbers are vendor claims.

- **PP-DocLayoutV3:** predicts multi-point boxes rather than two-point boxes, and
  reading order in a single forward pass. Apache-2.0. Run with PaddleOCR
  `LayoutDetection(model_name="PP-DocLayoutV3")` or the `paddleocr
  layout_detection` CLI, which Scribe already uses. Safetensors weights exist.
- **Jina OCR v1** (2026-09-14, arXiv 2609.03181) is **ruled out** for this
  pass:
  - built on DeepSeek-OCR (234.5% handwritten CER on METATR);
  - never evaluated on handwriting;
  - weights are CC BY-NC 4.0;
  - markdown only, no coordinates.

  It remains reachable through the generic `openai-compatible` client. It
  claims OmniDocBench v1.6 91.14 and olmOCR-Bench 83.4, but those are
  printed-document sets.
- **PaddleOCR-VL-1.6** (2026-05-28): PP-DocLayoutV3 layout, then a 0.9B VLM per
  element, merged in layout reading order into Markdown or JSON.
- **AWS Textract:** see §9.3.

## 4. Architecture

```
                 ┌──────────────── htr ────────────────────────────────────────┐
 formats ──────▶ │ pkg/ocrpage   Page model · readers · writers · IIIF · JSON  │
                 │ pkg/segeval   Evaluate(gt, hyp, profile) → Report           │
                 │ pkg/segment   Segmenter contract · registry · capabilities  │
                 │   ├ kraken  ppdoclayout  layoutkraken  remote(v2)           │
                 │   └ textract  azurelayout                                   │
                 │ pkg/pipeline  Segmenter + Recognizer → Page with text       │
                 │ pkg/providers Client (P4/P5 text), incl. openai-compatible, │
                 │               mistral-ocr                                   │
                 │ pkg/metrics   CER/WER (+ markdown normalization)            │
                 │ cmd/          segeval · segeval-external · convert · eval…  │
                 └─────────────────────────────────────────────────────────────┘
                                   ▲ go module (tagged release)
 Scribe: canonical IIIF validation, storage, editor, segmentor service, contexts
         (document_type), GT mark/export, correction metrics
```

## 5. Prior decisions carried forward

- Normalization: the hypothesis is scaled to the GT image size; polygons are
  compared with polygons, and otherwise both sides use boxes; explicit levels
  only.
- Matching: Hungarian per connected component of the overlap graph.
- Error classes: PRImA classes with directional merges.
- Profile-selected primary measurement.
- Calibration against downstream CER.
- Details are in §8.

## 6. `pkg/ocrpage`: page model, formats, IIIF

### 6.1 Types (`page.go`, `geometry.go`)

```go
type Point struct{ X, Y int }
type Rect struct{ X1, Y1, X2, Y2 int }        // exclusive max edges
type Level string                            // "region" | "line" | "word"
type Format string                           // "hocr" "pagexml" "alto" "iiif" "kraken" "htrjson" "txt"

type Page struct {
    Width, Height int
    Regions       []Region                   // reading order
    Source        Format
    Provenance    Provenance                 // provider, model, created; optional
}
type Region struct {
    ID         string
    Type       RegionType                    // canonical (§6.2)
    SourceType string                        // provider's original label, kept verbatim
    Box        Rect
    Polygon    []Point                       // optional
    Lines      []Line
    Table      *Table                        // optional; Type == RegionTable
    Field      *Field                        // optional; Type == RegionField (forms)
    Implicit   bool                          // synthesized; skipped at region level
    Confidence float64
}
type Line struct {
    ID         string
    Box        Rect
    Polygon    []Point
    Baseline   []Point                       // optional
    BaselineDerived bool                     // synthesized from a box; never used as cBAD GT
    Words      []Word
    Text       string                        // used when there are no words
    Handwritten *bool                        // when the provider says (Textract TextType)
    Confidence float64
}
type Word struct { ID, Text string; Box Rect; Polygon []Point; Confidence float64 }
type Table struct { Rows, Cols int; Cells []Cell }
type Cell  struct { Row, Col, RowSpan, ColSpan int; Box Rect; LineIDs []string }
type Field struct { KeyLineIDs, ValueLineIDs []string }   // key/value pairs (Textract FORMS)
type Element struct { ID string; Type RegionType; Text string; Box Rect; Polygon []Point; Baseline []Point; BaselineDerived bool; Order int }
```

Methods:

- `Page.Lines()`, `Page.Words()`, `Page.Elements(Level)` and `Page.HasLevel(Level)`.
- `Page.Scale(w, h)` scales every coordinate.
- `Page.PlainText()`, `Line.PlainText()` and `Region.Text()`.
- `FromLines(w, h, lines)` wraps lines in one implicit region.
- `Rect` helpers: `Area`, `Intersect`, `Union`, `Polygon`; plus
  `BoundingBox([]Point)`.

`Validate(Page, Limits)` rejects geometry outside the page, non-finite
confidences and counts above the limits. Every reader calls it.

### 6.2 Canonical region types (`types.go`)

The vocabulary is PAGE 2019 aligned, so that writing PAGE XML is lossless:
`text, heading, caption, header, footer, page-number, marginalia, footnote,
list, drop-capital, signature, table, field, image, math, separator, advert,
noise, unknown`.

Each provider and format package owns a mapping table from its labels to this
vocabulary, with a test that every documented label is mapped. Unknown labels
map to `unknown`, and the original is kept in `SourceType`. Misclassification
compares canonical types only.

### 6.3 Readers (`read_*.go`)

- **Entry point:** `Parse(data []byte, ReadOptions) (Page, error)` with
  auto-detection, plus `ParseFormat(data, Format, ReadOptions)`.
- **`ReadOptions`:** `{Limits; CanvasWidth, CanvasHeight int; DPI float64}`.
- **Limits** cover bytes, elements and depth. Defaults: 64 MiB, 1M elements,
  depth 256. Errors wrap `ErrLimit`. XML is parsed with `encoding/xml`;
  namespaces are ignored by local name, and HTML entities and auto-close are
  enabled for hOCR.

| Format | Detection | Mapping |
|---|---|---|
| hOCR | `ocr_page` class | `ocr_carea`/`ocr_par` → region; `ocr_line`/`ocrx_line` → line; `ocrx_word` → word. `bbox`, `x_wconf` (/100) and `baseline` (slope/offset → polyline) are read from `title` |
| PAGE XML 2013 and 2019 | `PcGts` root | `TextRegion@type` → canonical; `TableRegion` (+`TableCell`) → table; `ImageRegion`, `SeparatorRegion`, etc.; `TextLine/Coords@points`, `Baseline@points`, `Word`; `TextEquiv/Unicode`; `ReadingOrder/OrderedGroup` applied when present |
| ALTO v2–4 | `alto` root | `TextBlock` → region (`TAGREFS`/`@TYPE` when present); `TextLine` (`BASELINE` as a v4 points list or a v2/3 single y value); `String`/`SP`; `MEASUREMENTUNIT` `mm10`/`inch1200` converted with `DPI`, otherwise error; `Illustration`, `GraphicalElement` |
| IIIF AnnotationPage | JSON with `"type":"AnnotationPage"` | §6.5 |
| Kraken JSON | `"lines"` with `baseline`/`boundary` | Lines with polygon and baseline. `regions` (a map of type → list) → typed regions; lines are assigned to regions by maximum overlap. Baseline-only lines get a box from baseline extents with height max(20, Δy), marked `BaselineDerived=false` because the baseline is real, and the box is flagged in provenance. **Preserves current Scribe behavior**, including the existing tests |
| htr page JSON | `"htrPage": 1` | §6.6 |
| Plain text | fallback | Text lines with no geometry. Valid only for transcription metrics |

### 6.4 Writers (`write_*.go`), moved from Scribe

- **Functions:** `WriteHOCR(Page, HOCROptions)`, `WritePageXML(Page,
  PageXMLOptions)`, `WriteALTO(Page)`, `WriteText(Page)` and `WriteJSON(Page)`.
- **Byte-identical requirement:** for pages built from Scribe's
  `models.HOCRLine` (single implicit region, boxes only), output must equal
  today's Scribe `linesToPageXML`, `linesToALTOXML`, `linesToPlainText` and
  `hocr.ConvertHOCRLinesToXML`.
- **Options** carry Scribe's constants: PAGE `Creator="Scribe"`, fixed
  `Created`/`LastChange` `1970-01-01T00:00:00Z`, `imageFilename=source-image.png`,
  and hOCR `ocr-system` meta.
- **Escaping:** maps invalid XML runes to U+FFFD, then applies
  `html.EscapeString`, which matches Scribe's `escapeXMLContent`.
- **Richer pages:** when a page has polygons, baselines, region types, tables or
  reading order, the writers emit them (PAGE `Baseline`, `TableRegion`,
  `ReadingOrder`, typed `TextRegion`; ALTO `BASELINE`, `SHAPE/Polygon`; hOCR
  `baseline`).

### 6.5 IIIF crosswalk, moved from Scribe (`iiif.go`)

API:

```go
type IIIFReadOptions struct { CanvasWidth, CanvasHeight int; Limits Limits }
type IIIFReadResult  struct { Page Page; Ignored int /* non-OCR annotations */ ; Skipped []Skip /* reason, annotation index */ }
func ReadIIIF(data []byte, opts IIIFReadOptions) (IIIFReadResult, error)

type IIIFWriteOptions struct {
    CanvasID string
    ID       func(level Level, localID string) string     // Scribe keeps its ID scheme
    Levels   []Level                                    // which granularities to emit (default line+word)
}
func WriteIIIF(p Page, opts IIIFWriteOptions) ([]map[string]any, error)  // AnnotationPage items
func NormalizeAnnotation(anno map[string]any, defaultCanvas string) map[string]any
func ParseXYWH(fragment string) (Rect, error)
```

**Moves from Scribe**, with the same behavior:

- `normalizeAnnotation`, covering `@id`/`@type`, `resource.chars`, `on`,
  `bodyValue`, string bodies, `purpose` defaults and `textGranularity`
  defaults.
- `extractAnnotationText`, `parseXYWH`/`parseRoundedXYWH` (`pixel:`, float
  rounding, `percent:` rejected, limits) and `roundXYWHFragment`.
- `annotationPageToHOCRLinesWithDimensions`:
  - granularity rank page < block < paragraph < line;
  - comments/tags skipped and counted;
  - `assignWordsToLines` / `nearestLineForWord` / `wordsToLines` /
    `splitLineTextToWords`;
  - canonical item order kept as reading order.
- `buildLineAnnotations` / `buildWordAnnotations` become `WriteIIIF`.

**New in htr:**

- **Polygons and baselines** are read from `SvgSelector` (`<polygon points>`,
  `<polyline class="baseline" points>`). A target may carry an array of
  selectors; W3C treats multiple selectors as equivalent descriptions.
  `WriteIIIF` emits `[FragmentSelector xywh, SvgSelector]` when the page has
  them.
- **Region types** come from a body with `purpose: "classifying"` whose value is
  the canonical type (§11.5); `WriteIIIF` emits that body.

**Stays in Scribe:**

- canonical identity (public base URL, item image ID, page ID);
- `libops/iiif-spec` schema validation (htr stays stdlib-only);
- editor transforms (`annotation_page_transforms.go`), storage and
  reconciliation.

The internal `iiif.MaxPixelCoordinate` and `MaxAnnotationsPerPage` limits are
passed in through `Limits`.

**Parity:** `internal/server/annotation_crosswalk_test.go` (448 lines) and the
crosswalk cases in `hocr_parsing_test.go` are ported to
`pkg/ocrpage/iiif_test.go` as golden tests. Scribe keeps one parity test that
feeds its canonical fixtures through `ocrpage.ReadIIIF`.

### 6.6 htr page JSON (`json.go`)

`WriteJSON` and `ParseFormat(…, FormatHTRJSON)` are a lossless serialization of
`Page`: `{"htrPage":1, "width":…, "height":…, "regions":[…], "provenance":{…}}`.
It is the wire format of the `remote` v2 contract (§7.5) and the stored raw form
for `backfill`. A JSON Schema is committed at `pkg/ocrpage/htrpage.schema.json`.

## 7. Provider contracts

### 7.1 Segmenter (`pkg/segment/segment.go`)

```go
type Capabilities struct {
    Levels       []ocrpage.Level   // levels with geometry
    Polygons     bool
    Baselines    bool
    ReadingOrder bool
    RegionTypes  bool
    Text         bool              // P3 parsers fill Line/Word text
    Confidence   bool
    Tables       bool
    Fields       bool
}
type Hints struct {
    DocumentType string            // a segeval profile name; providers may tune features (§9.3)
    Languages    []string
}
type Request struct { Image providers.Image; Model string; Hints Hints }
type Result  struct {
    Page           ocrpage.Page
    EffectiveModel string
    Usage          providers.UsageInfo
    Raw            []byte          // native response, stored for backfill/re-parse
    RawFormat      string          // e.g. "textract-json", "kraken-json", "pagexml"
}
type Segmenter interface {
    Name() string
    Capabilities(model string) Capabilities
    Segment(ctx context.Context, req Request) (Result, error)
}
// FileSegmenter is optional; local subprocess providers implement it so callers
// that already hold a prepared file (Scribe's segmentor) avoid a copy.
type FileSegmenter interface { SegmentFile(ctx context.Context, path string, req Request) (Result, error) }
```

- Errors use the existing `providers.NewError` kinds, and the redaction rule
  applies everywhere: no upstream bodies, stderr, file paths or model paths
  appear in errors.
- Every provider has a `Parse<Native>(raw []byte, size image.Point) (ocrpage.Page,
  error)` function, so `backfill` and tests run without network calls.
- `segment.Registry` has the same shape as `providers.Registry`.

### 7.2 Recognizer

P4/P5 use the existing `providers.Client` (`Extract` → `Result{Text}`), with no
change to the contract. Kraken recognition (`RecognizeKraken`, moved from
Scribe) implements `providers.Client` over a line crop.

### 7.3 Capability gating

- `segeval` refuses a level the provider did not declare:
  `segment: provider "paddleocr-vl" has no line geometry; use --level region`.
- `eval` (transcription) accepts any provider with `Text`.

### 7.4 Pipeline (`pkg/pipeline`)

```go
type Options struct { Level ocrpage.Level /* crop unit, default line */; Concurrency int; Prompt string; Model string; PadPixels int }
func Run(ctx, seg segment.Segmenter, rec providers.Client, img providers.Image, opts Options) (ocrpage.Page, error)
```

- Segment the page, then crop each line using its box, or the polygon's bounding
  box plus padding. Cropping uses stdlib `image` `SubImage` with JPEG/PNG encode.
- Recognize each crop with bounded concurrency
  (`golang.org/x/sync/errgroup`, already an indirect dependency), then fill
  `Line.Text`.
- This makes any P1/P2 + P4/P5 combination an end-to-end system, for example
  Kraken + Gemini, Kraken + CATMuS, or PP-DocLayoutV3 + Kraken + Mistral OCR. Both
  segmentation and transcription can then be scored.
- `layout.Fill` (template fill) remains the alternative "whole page into a
  segmenter template" mode.

### 7.5 `remote` v2 contract (`pkg/segment/remote`, extends `pkg/remoteocr`)

- `POST /v2/segment` (multipart `image`, `model`, `hints` JSON) → 200 with
  htr page JSON (§6.6).
- `GET /v2/capabilities?model=` → `Capabilities` JSON.
- Same transport rules as `remoteocr` v1: exact endpoint, no redirects, bounded
  bodies, authenticator.
- The v1 `/v1/segment` boxes stay supported and map to a line-level page with
  boxes only.
- Scribe's segmentor service implements v2 (§11.1). Any Python model (Surya,
  Loghi, DocLayout-YOLO) can be served behind it with no Go changes.

## 8. `pkg/segeval`: measurement

### 8.1 Normalization

1. Scale the hypothesis to the GT `Width`/`Height`. If either side lacks
   dimensions, the error says which one.
2. Geometry mode: use `polygon` when both sides have polygons for every element
   at the level; otherwise `box`. The mode is recorded in the report.
3. Remove elements whose canonical type is in `Profile.Ignore ∪ Options.Ignore`
   from both sides.
4. Implicit regions are excluded at region level.
5. Baselines: cBAD runs only if every GT line at the level has a real baseline
   (at least two points and `!BaselineDerived`) and at least one hypothesis line
   has a baseline. If all hypothesis lines lack baselines, handwritten profiles
   use PRImA with `FallbackReason: "hypothesis has no baselines"`. Missing
   baselines in a partially populated hypothesis still count against cBAD.
6. Zero-area elements, including boxes collapsed by scaling, are dropped and
   counted separately in `Report.Dropped`.

### 8.2 Geometry engine (`raster.go`, `match.go`)

- **Rasterization:** shapes become per-row half-open intervals, scanline filled
  with the even-odd rule and sampled at row centers. The row step is
  `s = max(1, ceil(max(W,H)/4000))` pixels, and each row contributes
  `length × s` to area. Functions: area, pairwise intersection, union mask, and
  per-pixel multiplicity for MOTe.
- **Candidate pairs:** bounding-box overlap via a sort-and-sweep on x.
- **Assignment:** split the GT/hypothesis overlap graph into connected
  components, then run **Hungarian (Kuhn–Munkres)** per component, maximizing
  IoU, with pairs below IoU 0.05 excluded. This gives a one-to-one matching
  that is globally optimal.

### 8.3 Components (`errors.go`, `cbad.go`, `order.go`, `mote.go`, `columns.go`)

**A. PRImA classes.** τ = `Profile.Tolerance` (default 0.1). "Significant"
means the intersection is ≥ τ × the referenced element's area.

| Error | Rule | Area | Count |
|---|---|---|---|
| Merge, horizontal | A hypothesis significantly overlaps k ≥ 2 GT elements, and some pair among them has vertical-overlap ratio ≥ 0.5 (side by side) | Overlap with all but the largest GT | k−1 |
| Merge, vertical | As above, but the GT elements are stacked | same | k−1 |
| Split | A GT element is significantly covered by k ≥ 2 hypotheses | Overlap with all but the largest hypothesis | k−1 |
| Miss | GT coverage by the hypothesis union is < τ | GT area | 1 |
| Partial miss | Not a miss, but uncovered fraction > τ | Uncovered area | 1 |
| False detection | Hypothesis ∩ GT union is < τ × its area | Hypothesis area | 1 |
| Misclassification | Region level: matched pair with different canonical types | Overlap | 1 |

- Per type: `areaRate = weight × area / totalGTArea` and
  `countRate = weight × count / nGT`; `success = 1 − min(1, rate)`.
- **PRImA score** = the weighted harmonic mean of the area-based successes plus
  reading-order success, using the weights (PRImA's formulation). If any
  component with a non-zero weight is 0, the score is 0.
- The count-based variant is reported alongside.
  Weights intentionally affect both each error rate and the harmonic mean:
  increasing a weight increases the penalty and its contribution to the final
  score. These uncalibrated profile costs are policy choices, not an assertion
  of equivalence with an external PRImA implementation.

**B. cBAD**

1. Normalize polylines on the integer pixel grid with nominal five-pixel
   spacing, retaining at least 20 points for sufficiently long lines.
2. Tolerance per GT line is 25% of its estimated interline distance, capped at
   the mean detected interline distance. The reference's 250-pixel maximum
   supplies the fallback when interline estimation is unavailable.
3. Point distance is **Manhattan**, not Euclidean. Point score `c(d) = 1` if
   `d ≤ t`; `(3t − d)/(2t)` if `t < d < 3t`; `0` otherwise.
4. Recall uses GT-point coverage by the hypothesis union. Precision uses
   greedy one-to-one baseline alignment by descending coverage, with each
   unmatched hypothesis contributing zero. F is the harmonic mean of P and R.
5. A bidirectional Manhattan Chamfer distance is also reported; its defined
   flag is false when a side has no baseline points.

**Verified** against the Transkribus v0.1.3 reference executable, adaptive
mode. Four committed synthetic fixtures agree within 1e-3. See
`fixtures/segeval/cbad-reference.json` and `docs/SEGEVAL.md`.

**C. Reading order:** Kendall τ-b between GT order and hypothesis order over the
matched pairs, inversions counted by merge sort, then mapped to [0,1] via
`(τ+1)/2`. Fewer than 2 pairs gives 1.

**D. MOTe**, per the paper's definitions:

- Missing = 1 − (GT area covered by the prediction union) / GT area.
- Overlap = (Σ multiplicity − union) inside the GT, / GT area.
- Trespass = Σ_j (prediction j's area inside GT elements other than its
  assigned one) / GT area. Each prediction is assigned to the GT element of
  maximum intersection, ties to the lowest index.
- Total = M + O + T.
- Excess = prediction area outside the GT / non-GT page area.

**E. Matching:** from the Hungarian assignment, P/R/F1 at IoU 0.5 and 0.75,
mean F1 over 0.50:0.05:0.95, and mean matched IoU.

**F. Column integrity** (ledger, form):

- Cluster GT line x-intervals into columns: sort by center x, then merge
  intervals overlapping ≥ 50% of the narrower one.
- Score = the fraction of matched GT lines whose hypothesis's horizontal span
  stays within one column (±τ × column width).
- If the GT has `Table` cells, cells define the columns instead.

### 8.4 Profiles choose the measurement (`profile.go`)

```go
type Profile struct {
    Name, Description string
    Level       ocrpage.Level            // default line
    Primary     Measure                  // "prima" | "cbad" | "prima_region"
    Fallback    Measure                  // used when Primary's preconditions fail
    Tolerance   float64                  // τ, default 0.1
    Weights     Weights                  // per error type + ReadingOrder
    Ignore      []ocrpage.RegionType
    Supporting  []Measure                // "mote" "matching" "columns" "cbad" "prima_region"
    Calibrated  bool                     // false until §12 calibration lands
}
func Register(Profile) error; func Lookup(name string) (Profile, bool); func Profiles() []Profile
func LoadProfile(r io.Reader) (Profile, error)   // YAML or JSON (go.yaml.in/yaml/v3 is already a dependency)
```

| Profile | Primary | Fallback | Supporting | MergeH | MergeV | Split | Miss | Partial | FalseDet | Misclass | Order | Ignore |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| generic | prima | n/a | cbad*, mote, matching | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | none |
| printed | prima | n/a | mote, matching | 1 | 1 | 0.5 | 1 | 0.75 | 0.5 | 0.5 | 0.5 | none |
| newspaper | prima | n/a | prima_region*, mote, matching | 2 | 0.5 | 0.25 | 1 | 0.5 | 0.25 | 0.5 | 1 | image, advert |
| manuscript | cbad | prima | prima, mote, matching | 1 | 1 | 0.5 | 1 | 0.75 | 0.25 | 0.25 | 0.5 | noise |
| letter | cbad | prima | prima, mote, matching | 1 | 1 | 0.5 | 1 | 1 | 0.5 | 0.25 | 0.5 | none |
| ledger | prima | n/a | columns, mote, matching | 2 | 0.5 | 0.5 | 1.5 | 0.75 | 0.5 | 0.25 | 0.25 | separator |
| form | prima | n/a | columns, mote, matching | 2 | 0.5 | 0.5 | 1.5 | 0.75 | 0.25 | 0.25 | 0.1 | separator |

\* only when preconditions hold (baselines in the GT; regions in the GT).

Reasons for the less obvious weights:

- **Ledger:** entries are short and each matters. A cross-column merge
  attributes an amount to the wrong account. Ruling lines produce false
  detections.
- **Form:** merging a label with its value breaks key/value use. Forms are not
  read linearly.
- **Newspaper:** cross-column merges and order dominate; ads and rules are cheap
  false detections.

**The weights are uncalibrated** (`Calibrated: false`, shown in reports) until
§12.

### 8.5 API and report

```go
type Options struct { Profile Profile; Level ocrpage.Level /* override */; Ignore []ocrpage.RegionType }
func Evaluate(gt, hyp ocrpage.Page, opts Options) (Report, error)

type Report struct {
    Profile, Level, GeometryMode string
    Score          float64          // = the primary measure's value
    Primary        string
    FallbackReason string           // e.g. "ground truth has no baselines"
    Calibrated     bool
    PRImA          *PRImAResult     // per-type {Count, Area, AreaRate, CountRate, Success}, Score, CountScore
    PRImARegion    *PRImAResult
    CBAD           *CBADResult      // Precision, Recall, F, Chamfer
    MOTe           *MOTeResult      // Missing, Overlap, Trespass, Total, Excess
    Matching       *MatchingResult  // P/R/F1@0.5, @0.75, MeanF1, MeanIoU, Matched
    ReadingOrder   float64
    Columns        *float64
    Counts         struct{ GroundTruth, Hypothesis int }
}
```

Every computed component is in the report, whichever one is primary.
`segeval` performs no I/O.

## 9. Provider implementations

All providers follow `docs/PROVIDER_CLIENTS.md`:

- explicit options, an injected `*http.Client` via `httpclient.Secure`, and
  timeouts;
- bounded image, request and response sizes;
- redirect rejection and redacted typed errors;
- exact-payload tests against `httptest` servers.

A golden `Parse<Native>` test runs from a committed sample response under
`fixtures/providers/<name>/`. Sample responses are synthetic or from public
documentation; never commit customer content.

### 9.1 `kraken` (moved from Scribe `internal/worddetection/kraken.go`)

- Runs `kraken -i <img> <out.json> segment -bl -i <model>` with stdout/stderr
  discarded, and parses with `ocrpage` Kraken JSON, keeping polygons,
  baselines and regions.
- Implements `FileSegmenter`.
- Capabilities: Levels [line] (+region when the model emits regions);
  Polygons, Baselines, ReadingOrder.
- Tests moved from Scribe: baseline fallback, Kraken 7 native tags, malformed
  points, failure redaction (fake `kraken` on `PATH`).
- Also moves: `RecognizeKraken` (`kraken_ocr.go`), as `pkg/segment/kraken`
  `Recognizer`, a `providers.Client` over line crops, with its test.

### 9.2 `ppdoclayout` and `layout+kraken`

- **`ppdoclayout`** runs `paddleocr layout_detection -i <img> --model_name
  PP-DocLayoutV3 --model_dir <dir> --device <cpu|gpu> --save_path <tmp>`,
  exactly as Scribe does today, and reads the single JSON output (`boxes[]`:
  `label`, `coordinate`, `order`, `score`).
  - **VERIFY**: whether the multi-point box is present in the saved JSON, and
    under which key. If it is, populate `Region.Polygon`; otherwise use the
    4-value `coordinate` box.
  - Capabilities: Levels [region]; RegionTypes, ReadingOrder, Confidence
    (+Polygons if verified).
  - Label mapping: text → text; doc_title, paragraph_title → heading;
    figure_title → caption; header, header_image → header; footer,
    footer_image → footer; number → page-number; footnote, vision_footnote →
    footnote; aside_text → marginalia; image, chart → image; table → table;
    formula → math; seal → signature.
  - **VERIFY** the full label set from the model's config. Anything not listed
    maps to unknown and keeps its `SourceType`.
- **`layout+kraken`** is Scribe's `NewspaperProvider` logic, moved:
  - `parseLayoutRegions` keeps ranked regions and skips auxiliary unranked
    labels;
  - `orderNewspaperLines` assigns each Kraken line to the region of maximum
    overlap, so every line is kept exactly once, and unassigned lines come
    last in native order;
  - output is regions *with* their lines, plus region types.
  - Scribe's four newspaper tests move with it.

### 9.3 `textract` (AWS Textract `AnalyzeDocument`)

- **Dependency:** `github.com/aws/aws-sdk-go-v2/config` and
  `.../service/textract`. This is justified because SigV4 signing and the
  credential chain (env, profile, IMDS, IRSA) are security-sensitive and should
  not be hand-rolled. The client is injected via an interface for tests.
- **Features from the profile hint:** `LAYOUT` always; `TABLES` for ledger and
  form (and generic); `FORMS` for form; `SIGNATURES` for letter and form.
  `--textract-features` overrides.
- **Mapping:**
  - Geometry `BoundingBox`/`Polygon` (normalized 0–1) × image size; the image
    size comes from `image.DecodeConfig` on the same bytes.
  - `LAYOUT_TEXT` → text; `LAYOUT_TITLE`, `LAYOUT_SECTION_HEADER` → heading;
    `LAYOUT_HEADER` → header; `LAYOUT_FOOTER` → footer; `LAYOUT_PAGE_NUMBER` →
    page-number; `LAYOUT_LIST` → list; `LAYOUT_FIGURE` → image; `LAYOUT_TABLE`
    → table; `LAYOUT_KEY_VALUE` → field.
  - Layout blocks' `CHILD` relationships give their `LINE`s, and `LINE` →
    `WORD`; `WORD.TextType HANDWRITING|PRINTED` → `Line.Handwritten`
    (majority).
  - `TABLE`/`CELL` (RowIndex/ColumnIndex/spans; `CELL` children are words, and
    their lines are found) → `Region.Table`.
  - `KEY_VALUE_SET` KEY/VALUE → `Region.Field`. Confidence is /100.
  - Lines not under any layout block go into an implicit region.
  - Reading order is the order of the `LAYOUT_*` blocks (**VERIFY**).
- Capabilities: Levels [region, line, word]; Polygons, ReadingOrder,
  RegionTypes, Text, Confidence, Tables, Fields.
- **VERIFY:** the synchronous request size limits, accepted image formats and
  pixel limits, and handwriting language coverage. The client enforces the
  limits locally before the call.
- Also exposes `providers.Client` (`Extract` → plain text in reading order) so
  `htr eval` can score Textract transcription.

### 9.4 `azure-layout` (Document Intelligence `prebuilt-layout`, `api-version=2024-11-30`)

- Reuse the existing Azure analyze/poll code in `pkg/azure`. Add
  `ParseLayout(raw, size)` and a `Segmenter`.
- **Mapping:**
  - `pages[0].lines[].polygon`/`words[].polygon` are flat `[x1,y1,…]` in pixels
    for images.
  - `paragraphs[].role`: title → heading, sectionHeading → heading, pageHeader
    → header, pageFooter → footer, pageNumber → page-number, footnote →
    footnote, none → text. Each paragraph's `boundingRegions` becomes a region,
    in reading order.
  - A line is assigned to the paragraph whose `spans` contain the line's spans.
  - `tables[].cells` → `Region.Table`; `figures` → image regions; word
    confidence is kept.
- Capabilities: Levels [region, line, word]; Polygons, ReadingOrder,
  RegionTypes, Text, Confidence, Tables.

### 9.5 `openai-compatible` (generic P4 access)

- The existing `pkg/openai` client already takes `Endpoint` and `APIKey`
  options. Add a CLI/registry provider `openai-compatible` with `--endpoint`
  (required) and `--api-key-env` (the env var name, default
  `OPENAI_COMPATIBLE_API_KEY`). It reuses all existing bounds and redaction.
- Purpose: run evaluate-first candidates served by vLLM (e.g. Churro) through
  `htr eval` without new code.
- **Ship no presets, prompts or docs for ruled-out models** (ROADMAP §4), and
  no model-specific code paths.
- Text only: no segmenter is registered.
- Documentation addition (no code): recommend **Qwen3-VL 8B through the
  existing `ollama` provider** as the best open model on METATR.

### 9.5a `mistral-ocr` (Mistral OCR API)

- New package `pkg/mistral` implementing `providers.Client`, following
  `docs/PROVIDER_CLIENTS.md`: explicit options, `httpclient.Secure`, bounded
  sizes, redirect rejection, redacted errors, and exact-payload tests.
- **Expected shape (VERIFY against Mistral's API reference before coding):**
  - request: `POST https://api.mistral.ai/v1/ocr` with `{"model":
    "mistral-ocr-latest", "document": {"type": "image_url", "image_url":
    "data:<media>;base64,…"}}`, authenticated by a Bearer API key
    (`MISTRAL_API_KEY`);
  - response: `pages[].markdown`, `pages[].dimensions`, `pages[].images[]`
    (figure boxes only) and `usage_info`.
- `Extract` returns the concatenated page markdown as `Text`. Callers score it
  with `--normalize markdown` (§9.6), which `eval` defaults to for this
  provider and records in the YAML config.
- Figure boxes are ignored, and no segmenter is registered: the API gives no
  line or word geometry.
- `Usage`: pages processed, recorded so `htr cost` can price per page.

### 9.6 Markdown normalization for transcription metrics (`pkg/metrics`)

P4 models emit markdown, HTML tables and LaTeX. To compare fairly with
plain-text GT, add `metrics.Options.Normalize`:

- `none` (default; current behavior unchanged).
- `markdown`, which strips:
  - ATX/setext headings, emphasis markers, inline code fences, list bullets
    and numbering, blockquote markers and link syntax (`[t](u)` → `t`);
  - `<table>`/`<tr>`/`<td>` and other HTML tags (cell text kept,
    space-separated) and image syntax;
  - the `$…$` / `$$…$$` delimiters, keeping their content.
- Then collapse whitespace per line.

`htr eval` and `eval-external` get `--normalize none|markdown`, saved in the
config and honored by `backfill` like `--single-line`. Table-driven tests cover
each construct.

### 9.7 Evaluate-first models: no client code in this pass

Transkribus, Google Document AI, PaddleOCR-VL-1.6, dots.mocr, Chandra OCR 2,
Surya OCR 2, Churro, PeroOCR, Laypa/Loghi, DocLayout-YOLO and Docling Heron are
evaluated from exported outputs with `segeval-external` / `eval-external`. The
procedure and open questions for each are in ROADMAP §3.

This pass must make that evaluation possible without per-model code. The
generic readers (PAGE, ALTO, hOCR, IIIF, Kraken, htr page JSON), the
`openai-compatible` client and the `remote` v2 contract cover every candidate.
A candidate that passes the ROADMAP §1 gate gets its own provider in a later
change.

## 10. CLI: same ergonomics as `htr eval`

Mirror the transcription commands one-for-one. Flag names, short flags, CSV
handling (`--csv/-c`, `--dir`, `--rows`), `--config` reruns, YAML result files,
the long help style with worked examples, and the `summary` / `csv` / `backfill`
workflow are all identical.

| Transcription today | Segmentation (new) |
|---|---|
| `htr eval --provider X --model/-m M --csv/-c data.csv [--dir] [--rows] [--config prev.yaml] [--timeout]` | `htr segeval --provider kraken\|ppdoclayout\|layout+kraken\|textract\|azure-layout\|remote\|… --model/-m M --csv/-c data.csv [--dir] [--rows] [--config prev.yaml] [--timeout] [--profile P] [--profile-file f.yaml] [--level line] [--ignore-type T]… [--recognizer X --recognizer-model M]` |
| CSV `image,transcript,public` | CSV `image,ground_truth,profile,public` (`profile` optional per row; otherwise `--profile`, otherwise `generic`) |
| `htr eval-external --csv f.csv --name/-n loghi [--dir] [--rows]` | `htr segeval-external --csv f.csv --name/-n kraken-blla [--dir] [--rows] [--profile P] [--level] [--ignore-type]` |
| CSV `transcript,transcription` | CSV `ground_truth,segmentation,profile`: any format on either side, auto-detected. This is the format of Scribe's GT export |
| `--ignore '\|'` | `--ignore-type decoration` (repeatable; adds to the profile's ignore list) |
| `--single-line` | `--level line\|word\|region` |
| `--normalize` (new, §9.6) | n/a |
| Results `evals/<model>.yaml`: `config` + `results[]` with `providerresponse` | `evals/segmentation/<model>.yaml`: `config` (provider, model, profile defaults, level, csv, rows, timestamp, `kind: segmentation`) + `results[]` (identifier, paths, profile, level, geometry_mode, score, primary, fallback_reason, all components). Raw provider output is saved at `evals/segmentation/<model>/<identifier>.<ext>`, the counterpart of `providerresponse` |
| `htr summary [file]` | Lists transcription and segmentation files; a segmentation summary prints averages per profile and per component |
| `htr csv` | `htr csv --kind segmentation`: sorted by mean score, one column per component |
| `htr backfill` | Also recomputes segmentation results from the stored raw outputs plus the GT (honors `--profile-file` overrides), e.g. after calibration |
| `htr cost` | Applies to providers that report token usage (P3 VLM parsers); Textract and Azure report none |

When `--recognizer` is set, `segeval` runs the §7.4 pipeline. If the CSV also has
a `transcript` column, the result records **both** segmentation components and
transcription metrics, giving one end-to-end evaluation file.

Conveniences that reduce to a one-row CSV internally:

- `htr segeval-external --gt a.xml --segmentation b.json --profile letter`
  scores a single pair;
- `htr segment --provider kraken -m blla image.jpg --to pagexml` runs a
  segmenter;
- `htr convert --in page.json --to pagexml|alto|hocr|iiif|htrjson|txt`
  converts formats.

Provider environment variables follow the existing pattern and are documented
in ARCHITECTURE.md:

- `AWS_*` / profile for Textract;
- `AZURE_OCR_*`, already used;
- `KRAKEN_SEGMENTATION_MODEL`;
- `PADDLE_LAYOUT_MODEL_DIR`;
- `HTR_REMOTE_SEGMENT_URL`;
- `OPENAI_COMPATIBLE_API_KEY`;
- `MISTRAL_API_KEY`.

## 11. Scribe changes

### 11.1 Consume htr

| Scribe today | Change |
|---|---|
| `annotation_crosswalk.go` writers, `internal/hocr/converter.go` | `ocrpage` writers via `internal/ocradapter` (`models.HOCRLine ↔ ocrpage`); `renderAnnotationExport` stays the single export boundary; exports byte-identical |
| `annotationPageToHOCRLinesWithDimensions` + IIIF helpers (`annotations.go`, `annotation_actions.go`) | Validate with Scribe's canonical rules, then `ocrpage.ReadIIIF`; the helpers are deleted, and remaining callers use `ocrpage.NormalizeAnnotation` / `ParseXYWH` |
| `buildLineAnnotations` / `buildWordAnnotations` | `ocrpage.WriteIIIF` with Scribe's ID function |
| `internal/worddetection/kraken.go`, `newspaper.go`, `kraken_ocr.go` | `htr/pkg/segment/{kraken,layoutkraken}`; `worddetection.Provider` becomes an adapter that flattens to `WordBox`; `ValidateBoxes` stays |
| `internal/segmentor` | Routes models via `segment.Registry` and **adds `/v2/segment` and `/v2/capabilities`**, returning htr page JSON with polygons, baselines and region types. v1 is unchanged |
| `internal/providerregistry` | Adds `mistral-ocr` (§9.5a) and `openai-compatible` (§9.5) as transcription providers, with endpoint policy `EndpointVendor` and a configured URL, plus `textract` and `azure-layout` as segmentation providers through configuration. Self-hosted containers (PaddleOCR-VL, Surya) are out of scope; they arrive later through `remote` v2 and Terraform |
| `internal/hocr/parser.go` | Unchanged in this pass |

Scribe's segmentation model catalog (`config/ocr.yaml`, `config/catalog.go`)
gains entries for `ppdoclayout` and `layout+kraken`. The existing `newspapers`
ID stays as an alias of `layout+kraken`. Following the Scribe ops rule, any new
service is Terraform only, with no deploy scripts.

### 11.2 Document type (profile) on contexts and items

- **Proto:**
  - `Context.document_type` (string, required, pattern `^[a-z][a-z0-9_-]{0,63}$`);
  - `Item.document_type` (optional override; empty means inherit);
  - regenerate with buf.
- **Validation:** the server checks values against `segeval.Profiles()`, so the
  registry in htr is the single source of truth.
- **DB migration:**
  - `contexts.document_type VARCHAR(64) NOT NULL DEFAULT 'generic'`;
  - `items.document_type VARCHAR(64) NULL`;
  - backfill `newspaper` where `segmentation_model = 'newspapers'`;
  - update the sqlc queries.
- **API:** a list endpoint or field so the UI can fetch the profile names and
  descriptions.
- **UI:**
  - a "Document type" select on the context form, with help text: "Controls
    how segmentation quality is measured and which layout features are
    requested";
  - an override select on the item.
- **Hints:** the resolved document type is passed as `segment.Hints.DocumentType`
  to every segmentation call, which for example picks Textract features.

### 11.3 Ground truth from Scribe

- **Mark:** a "Mark as ground truth" / "Unmark" action on a page revision,
  stored as `annotation_revisions.ground_truth BOOLEAN` with
  `ground_truth_marked_by` / `_at`. It is an explicit action, so half-corrected
  pages are never ground truth. Editing a GT page creates a new revision that is
  not marked, and the UI offers to re-mark it.
- **Export:** a GT bundle per context or batch, as a zip or a GCS prefix:
  - `pages.csv` with columns `ground_truth,segmentation,profile,image,transcript`;
  - for each page: the GT AnnotationPage JSON, the model's original
    segmentation (the OCR run already kept for correction metrics,
    `triplet_presentation.go`, stored as htr page JSON when available), the
    plain-text transcript, and the image or its IIIF URL.
  - `htr segeval-external --csv pages.csv` consumes the bundle unchanged.
- **In-app metric:** on save, `segeval.Evaluate(original, corrected,
  profile)` runs, and the component scores are stored with the existing
  correction telemetry, giving segmentation-correction metrics next to the text
  metrics.

### 11.4 Baselines and polygons (enables cBAD on Scribe GT)

- When a segmenter returns polygons or baselines, Scribe stores them in the
  canonical annotation as a second selector: `target.selector =
  [FragmentSelector xywh, SvgSelector]`, written by `ocrpage.WriteIIIF`. The
  `xywh` box remains authoritative for viewers that ignore SVG.
  - **VERIFY** that `libops/iiif-spec` validation accepts selector arrays.
    Extend validation if not.
- **Editor:**
  - moving a line translates its polygon and baseline;
  - resizing scales them;
  - a line drawn fresh has none (never guessed);
  - merge and split of lines concatenate or cut baselines at the split x.
- Until a page has real baselines, handwritten profiles fall back to PRImA, and
  the report says so.

### 11.5 Region types

Region (`block`) annotations from layout-capable segmenters are kept in the
canonical page with a `purpose: "classifying"` body carrying the canonical type.
This makes region-level and misclassification scoring possible on Scribe GT.
The editor shows the type and allows changing it.

## 12. Calibration (last step)

On Scribe GT pages that have both a corrected layout and a corrected transcript:

1. Run each configured segmenter through the pipeline with a fixed recognizer.
2. Compute CER with `pkg/metrics`.
3. Fit each profile's weights (non-negative least squares on per-type rates
   against CER) so that a higher score means lower CER.
4. Commit the weights, the Spearman ρ and the page counts to
   `pkg/segeval/profiles.go` and `docs/SEGEVAL.md`, and set `Calibrated: true`.

Fewer than 30 GT pages for a profile leaves it uncalibrated. This step needs
GT, so it can only run after §11.3 has shipped and Scribe users have marked
pages. It is a follow-up task, not a blocker for this pass.

## 13. Implementation order, files, and acceptance

| # | Repo | Deliverable | Files (new unless noted) | Acceptance |
|---|---|---|---|---|
| 1 | htr | Page model and readers | `pkg/ocrpage/{page,geometry,types,limits,xml,read_hocr,read_pagexml,read_alto,read_kraken,json,detect}.go` + tests, `htrpage.schema.json` | Round-trip per format; each reader fuzzed (`FuzzParse`) for panics and limits; PAGE 2013/2019 and ALTO v2/v4 fixtures |
| 2 | htr | Writers | `pkg/ocrpage/write_{hocr,pagexml,alto,text}.go` | Golden files copied from Scribe export tests are byte-identical |
| 3 | htr | IIIF crosswalk | `pkg/ocrpage/iiif.go` + ported tests | Every case ported from Scribe's crosswalk tests passes unchanged |
| 4 | htr | `segeval` core | `pkg/segeval/{raster,match,hungarian,errors,order,mote,columns,report,evaluate}.go` | One fixture per error class yields exactly that error; GT vs itself = 1.0 for every component; Hungarian matches brute force on random ≤ 7×7; MOTe agrees with `mote_error` on 3 shared fixtures (values committed) |
| 5 | htr | cBAD | `pkg/segeval/cbad.go` | After VERIFY: agrees with the reference implementation on committed sample pages within 1e-3 |
| 6 | htr | Profiles | `pkg/segeval/profile.go`, `docs/SEGEVAL.md` | All 7 profiles registered; YAML load round-trips; fallback reason reported |
| 7 | htr | Segment contract + kraken + layout | `pkg/segment/{segment,registry}.go`, `pkg/segment/kraken/`, `pkg/segment/ppdoclayout/`, `pkg/segment/layoutkraken/` | Scribe's moved tests pass; redaction test passes |
| 8 | htr | Remote v2 | `pkg/segment/remote/` | httptest contract tests; v1 fallback |
| 9 | htr | **Azure layout first**, then Textract | `pkg/azure/layout.go`, `pkg/segment/textract/` | `Parse*` goldens from documented sample responses; exact-payload tests |
| 10 | htr | Mistral OCR, openai-compatible, normalization | `pkg/mistral/`, `cmd` registry entries, `pkg/metrics/normalize.go` | Markdown normalization table tests; `eval --normalize markdown` recorded in YAML; Mistral exact-payload and response-bound tests; no presets for ruled-out models |
| 11 | htr | Pipeline | `pkg/pipeline/` | Stub segmenter + stub recognizer produce the expected page; concurrency bounded; ctx cancel |
| 12 | htr | CLI | `cmd/{segeval,segeval-external,segment,convert}.go`; extend `summary`/`csv`/`backfill` | CLI golden tests on committed fixtures; the same page as Kraken JSON vs PAGE vs IIIF gives identical scores; `backfill` recomputes from raw outputs |
| 13 | htr | Evaluate-first readiness (no per-model code) | Fixtures only | `segeval-external` scores a PAGE XML export (Transkribus/Laypa style, with baselines) and an htr-page-JSON export (Surya/PaddleOCR-VL converted) end to end |
| 14 | htr | Docs | ARCHITECTURE.md, PROVIDER_CLIENTS.md, README, CLAUDE.md commands; link ROADMAP.md from README | Docs match the flags (`--help` output); recommended-models section cites ROADMAP §2 |
| 15 | htr | Release | tag minor | Scribe builds against the tag |
| 16 | Scribe | Consume htr (§11.1) | `internal/ocradapter/`; edits in `internal/server`, `internal/worddetection`, `internal/segmentor`, `internal/providerregistry`; `go.mod` | Full Scribe suite green with **no changed expectations** in export tests |
| 17 | Scribe | Document type (§11.2) | proto, migration, sqlc, server validation, UI | Migration up/down; invalid type rejected; newspaper backfill |
| 18 | Scribe | GT mark + export + in-app metric (§11.3) | revisions schema, RPCs, export job, telemetry | Export CSV consumed by `htr segeval-external` in an acceptance test |
| 19 | Scribe | Baselines/polygons + region types (§11.4–11.5) | annotation write path, editor transforms, UI | Move/resize/split/merge tests keep baselines consistent; iiif-spec validation passes |

Local development: htr and Scribe are linked through an **uncommitted**
`go.work` (in the scratch area), then the htr tag (step 15) is required before
the Scribe `go.mod` bump is committed (currently `htr v0.23.1`).

## 14. Out of scope

Future work, the evaluate-first queue, and the ruled-out list are tracked in
[ROADMAP.md](ROADMAP.md).

- Table-structure metrics (GriTS/TEDS): `Region.Table` is stored, but not scored.
- No-ground-truth layout metrics (Beyene & Dancy).
- Self-hosted model containers for PaddleOCR-VL, Surya, dots and Chandra in
  Scribe's infrastructure: the `remote` v2 contract makes them drop-in later.
- Moving Scribe's hOCR parser.
- Calibration runs (§12), which need marked GT.

## 15. Facts to VERIFY before coding the dependent piece

1. cBAD: tolerance derivation and R/P alignment (§8.3B). Sources:
   arXiv:1705.03311 and the Transkribus Java reference implementation.
2. PP-DocLayoutV3: the saved-JSON keys for multi-point boxes, and the full label
   set (§9.2).
3. Textract: reading order of `LAYOUT_*` blocks; sync size, format and pixel
   limits; handwriting languages (§9.3).
4. Mistral OCR API: endpoint, request/response schema, size limits and pricing
   unit (§9.5a).
5. Azure `prebuilt-layout` at `api-version=2024-11-30`: field names for
   paragraph roles, spans and polygons on image input (§9.4).
6. `libops/iiif-spec`: whether selector arrays are accepted (§11.4).

Open questions about evaluate-first models (PaddleOCR-VL, dots, Chandra,
Transkribus, Surya) are tracked in ROADMAP §3. They do not block this pass.

## Sources

- PRImA, ICDAR 2011: https://www.primaresearch.org/www/assets/papers/ICDAR2011_Clausner_PerformanceEvaluation.pdf ; source: https://github.com/PRImA-Research-Lab/prima-layout-eval
- READ-BAD / cBAD: https://arxiv.org/abs/1705.03311
- MOTe: https://arxiv.org/abs/2603.12718 ; https://github.com/THE-3TC/mote_error
- PP-DocLayoutV3: https://huggingface.co/PaddlePaddle/PP-DocLayoutV3
- Jina OCR v1: https://jina.ai/news/jina-ocr-v1-faster-document-parsing-on-low-budget-gpus/
- PaddleOCR-VL: https://www.paddleocr.ai/latest/en/version3.x/pipeline_usage/PaddleOCR-VL.html
- Chandra OCR 2: https://github.com/datalab-to/chandra
- dots.mocr (gateway docs): https://docs.vlm.run/gateway/models/rednote-hilab-dots-mocr.md
- Surya: https://pypi.org/project/surya-ocr/0.4.7
- Transkribus Metagrapho: https://transkribus.org/en/metagrapho
- OCR model roundups (vendor/secondary; used for leaderboard context only): https://www.docsumo.com/blog/best-ocr-models , https://roboflow.com/blog/best-open-source-ocr-models
- Handwriting roundups (secondary): https://www.llamaindex.ai/insights/best-ai-for-handwriting-recognition , https://arxiv.org/pdf/2609.20064
- OCR-D ocrd-segment: https://pypi.org/project/ocrd-segment

## Implementation status (2026-10-28)

The htr working tree now includes `ocrpage` readers/writers and IIIF selectors,
profile-driven `segeval`, cBAD/MOTe reference fixtures, the segment contract and
tier-one providers, crop pipelines, Mistral OCR, generic OpenAI-compatible
access, markdown normalization, conversion/evaluation commands and saved-result
summary/CSV/backfill. The command and library guide is [SEGEVAL.md](SEGEVAL.md).
Scribe's four existing box-only export goldens pass unchanged in htr.

Verified corrections to the cBAD summary in §8.3B: the reference uses Manhattan
point distance, integer baseline normalization with nominal spacing 5 and at
least 20 points for sufficiently long lines, adaptive tolerances at 25% of
capped GT interline distances, union recall and greedy one-to-one precision.
Four synthetic cases agree with the v0.1.3 reference executable within 1e-3.
Three MOTe cases agree with the pinned `mote_error` implementation; overlapping
GT pixels use first-element ownership.

PP-DocLayout polygons are saved under `polygon_points` in PaddleX's native
layout result; `coordinate` remains the rectangle fallback. Azure and Mistral
request/response fields were checked against their official API documentation.
The current Mistral API also advertises paragraph blocks; this implementation
exposes markdown only and makes no text-geometry capability claim.

The Scribe working tree now consumes the shared exports, native Kraken
character-cut recognition and v2 segmentation geometry. All original crosswalk
cases and v1 Kraken/newspaper tests are ported into htr. Legacy inferred words
are restricted to text exports. Context/item document profiles, immutable GT
mark/export, correction telemetry, editor point transforms, baseline splits and
joins, and region classification controls are implemented. The existing four
Scribe export goldens and expectations remain unchanged.

Scribe has no historical `annotation_revisions` table: its single editable
AnnotationPage is revisioned in place. Explicit marks therefore create immutable
`annotation_ground_truth` snapshots, with quota accounting and resource cleanup.
Exports include both canonical IIIF and dimensioned htr GT JSON, original
segmentation, transcripts, image URLs and a ready-to-score CSV. Interactive
scoring is capped at 256 elements per side and two evaluations per process;
larger pages explicitly request offline scoring. Schema migrations follow
Scribe's existing forward-only migration contract.

Still pending before release:

- Add a minor release/tag after review, pin Scribe to it, and run its full
  isolated CI against that dependency (step 15 and final integration gate).
  Scribe's pre-release CI retry against v0.23.1 fails during lint because that
  dependency lacks `pkg/ocrpage`. Do not treat local workspace tests as
  completion of this gate. Run against the published dependency and compatible
  lint toolchain after updating the pin.
- Run calibration when enough real GT pages exist; it remains a follow-up as
  stated in §12. No live provider benchmark or accuracy claim is inferred from
  the synthetic acceptance tests.
