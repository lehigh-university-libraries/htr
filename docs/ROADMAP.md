# htr roadmap: model evaluation and future providers

Last updated 2026-10-28. This file records published evidence about OCR, HTR and
layout models and the decisions it drove, so we don't re-research or integrate
models that others have already ruled out. The implementation plan is in
[segeval-proposal.md](segeval-proposal.md). Update this file when new evidence
appears.

## 1. Integration gate (applies to every new provider)

A provider client is written **only** when one of these is true:

1. **It wins on our pages.** Its outputs, run by hand and scored with
   `htr eval-external` (text) or `htr segeval-external` (segmentation) on Scribe
   ground-truth pages, beat the current best for at least one profile.
2. **It fills a capability gap** a profile needs and no supported provider
   covers, for example table/field geometry for `ledger` and `form`.
3. **Independent evidence** on historical or handwritten material puts it at or
   near the top, as with METATR (§2.1).

Leaderboards built from modern, mostly printed digital documents
(OmniDocBench, olmOCR-Bench) **do not count** as evidence for Scribe's
material (§2.2).

Running a candidate costs no htr code. Any tool that exports PAGE XML, ALTO,
hOCR, IIIF or Kraken JSON is scored by `segeval-external`, and any tool's text
by `eval-external`.

## 2. Findings

### 2.1 METATR: the benchmark that matches our material

Boillet, Tarride & Kermorvant (Teklia), "METATR: A Multilingual, Evolving
Benchmark for Automatic Text Recognition", arXiv:2605.26712, May 2026.

- **Contents:** 17 datasets across 29 languages; **63% handwritten**, mostly
  historical. Collections include:
  - READ-2016 (German administrative records, 1470–1805) and Esposalles
    (17th-century Spanish marriage records);
  - ScribbleLens (Dutch ship logbooks) and HORAE (books of hours);
  - NorHand (Norwegian letters and diaries) and DAI-CRETDHI (French parish
    records);
  - RASM and IAM/RIMES, among others.
- **Dataset:** huggingface.co/datasets/Teklia/ATR-benchmark.

Page-level CER, % (Table 4). Lower is better; above 100% means the model is
inventing text.

| System | Printed | Handwritten | htr status |
|---|---|---|---|
| Gemini 3 Pro | **17.6** | **28.5** | supported (`gemini`) |
| Claude Opus 4.5 | 53.5 | 31.2 | supported (`claude`) |
| Mistral OCR | 56.1 | 43.6 | **added in this pass** (`mistral-ocr`) |
| Azure Layout | 30.8 | 49.1 | **promoted**: `azure-layout` with geometry |
| Qwen3-VL 8B | 31.6 | 54.9 | supported via `ollama` (no new code) |
| Churro 3B | 41.0 | 58.1 | evaluate-first (§3) |
| Mistral large / medium | 56.6 / 57.9 | 64.3 / 60.0 | not planned |
| GPT-5.1 | 58.9 | 59.5 | supported (`openai`) |
| PeroOCR | 53.2 | 73.9 | evaluate-first (§3) |
| RolmOCR 8B | 51.1 | 89.6 | **ruled out** |
| olmOCR-2 8B | 38.6 | 95.3 | **ruled out** |
| DeepSeek-OCR-2 3B | 67.5 | 104.4 | **ruled out** |
| DeepSeek-OCR 3B | 72.3 | 234.5 | **ruled out** |
| LightOnOCR-2 1B | 65.2 | 255.7 | **ruled out** |
| Mistral small | 66.1 | 280.7 | **ruled out** |

Not evaluated in METATR: Transkribus, Kraken, Textract, Google Document AI,
PaddleOCR-VL, dots, Chandra, Jina OCR v1, Surya.

Caveats:

- This is one study, and it tested older model versions (htr now targets
  `gemini-4-argon`).
- CER is measured per page, so reading-order errors on multi-column pages
  inflate it.
- The hallucination counts come from only 5 efficiency pages.

### 2.2 Document-parsing leaderboards do not transfer

- **OmniDocBench v1.6:**
  - PaddleOCR-VL-1.6 96.34, MinerU2.5-Pro 95.75, GLM-OCR 95.22;
  - TeleOCR 96.91 and OvisOCR2 96.47 are reported by third parties;
  - Jina OCR v1 scores 91.14.
- **olmOCR-Bench:** Chandra OCR 2 ~85.8, dots.mocr 83.9, LightOnOCR-2 83.2,
  Jina OCR v1 83.4, olmOCR-2 82.4.
- These sets are modern, mostly printed, digital documents. METATR shows the
  gap: olmOCR-2 and LightOnOCR-2 rank well there but reach 95–256% CER on
  historical handwriting.
- The original OmniDocBench paper (2024) also found most VLMs fail on
  newspapers, where pipeline tools do much better.

### 2.3 Other handwriting evidence (weaker)

- **arXiv:2502.20295 (IAM, modern):** among cloud OCR baselines, Azure did best
  and cheapest, ahead of Google Cloud Vision and Amazon Textract (Azure CER
  0.036).
- **AIMultiple cursive test:** 100 modern samples, scored by cosine similarity
  rather than CER, page updated 2026-08. Gemini 3 Pro was top, then GPT-5 and
  olmOCR-2. Textract, Azure and Google landed mid-pack. PaddleOCR-VL and dots
  were tested, but their scores are not stated. This is weak evidence.
- **arXiv:2604.16504 (handwritten medical forms):** 17 LLMs only. Gemini 3.1
  was best overall (free-text CER 0.31); GPT 5.4 was best on noisy dates.
  Cloud OCR engines were not tested.
- **Transkribus Text Titan II:** the only figure is a vendor claim of 47% lower
  CER than Titan I on an internal set. There is no independent comparison.

### 2.4 Segmentation and layout: no head-to-head evidence exists

- PP-DocLayoutV3 has no independent benchmark yet; only a proposal exists
  (github.com/xberg-io/xberg/issues/2284). PP-DocLayout v1 reported 75.2% mAP
  (M model).
- DocLayout-YOLO reports mAP@50:95 of 73.1 on PRIMA-LAD, but it outputs no
  reading order.
- There is no shared-test-set baseline-F comparison of Kraken BLLA, Laypa/Loghi
  and Transkribus. The only data point is anecdotal: BLLA left 14% of lines
  empty on 6 Hertwig pages (github.com/thodel/agentic_historian/pull/707).
- **Implication:** our own `segeval` on Scribe GT is the only way to choose
  segmenters. Treat calibrated results as a contribution worth publishing.

## 3. Evaluate-first queue (no client code until the gate in §1 passes)

| Candidate | Paradigm | Geometry | Why it's queued | How to evaluate | Open questions |
|---|---|---|---|---|---|
| Transkribus (Text Titan II, layout) | grounded parser | region, line (baselines), word | Specialist incumbent; no independent numbers | Run on a Scribe GT sample in the Transkribus UI, export PAGE XML, then score with `segeval-external` and `eval-external` | API endpoints and model IDs; credit cost |
| PaddleOCR-VL-1.6 | grounded parser | region only | Top on OmniDocBench; claims robustness on historical material, but no data | Run the PaddleOCR pipeline locally, convert its JSON, then score with `eval-external` (text) and `segeval-external --level region` | Result JSON fields; license for 1.6 |
| Chandra OCR 2 | grounded parser | region only | Top on olmOCR-Bench; strong reported handwriting and tables | Run via Datalab API or self-hosted; text with `eval-external` | Modified OpenRAIL-M; commercial self-hosting license |
| dots.mocr | grounded parser | region only | Layout + text in one call | Run on vLLM; text and region scores | Schema (`bbox_xywh` normalized?); license (MIT?) |
| Surya OCR 2 | line + layout + parser | region, line (polygons), order | The only open model that does line detection + layout + order | Run the CLI and convert to htr page JSON; score at line level | Weights license for v2 |
| Churro 3B | text VLM | none | Open, historical-focused; mid-table on METATR | Run on vLLM via `openai-compatible` | Prompt; license |
| PeroOCR | line OCR | line | Mid-table on METATR; open | Export PAGE XML, then score | n/a |
| Google Document AI OCR | grounded parser | region, line, word | Scribe is on GCP; no historical data | Export JSON; score once a `Parse` exists, or convert by hand | OAuth dependency |
| Laypa / Loghi | line segmenter | line (baselines) | Archival-focused open segmenter | Export PAGE XML, then score with `segeval-external` (cBAD) | n/a |
| DocLayout-YOLO, Docling Heron | layout detector | region | Alternatives to PP-DocLayoutV3 | Serve behind `remote` v2 or export, then score at region level | No reading order (YOLO) |

When a candidate passes the gate, move it into a numbered step of the
implementation plan, add its provider per `docs/PROVIDER_CLIENTS.md`, and
record the result here.

## 4. Ruled out (do not integrate)

| Model | Reason | Revisit if |
|---|---|---|
| olmOCR-2, RolmOCR | 95.3% / 89.6% handwritten CER on METATR | A new version publishes historical-handwriting results |
| DeepSeek-OCR, DeepSeek-OCR-2 | 234.5% / 104.4% handwritten CER | Same |
| LightOnOCR-2 | 255.7% handwritten CER | Same |
| Jina OCR v1 | Built on DeepSeek-OCR; not evaluated on handwriting. **Inferred** weak; CC BY-NC weights | It publishes METATR-class results, or we need a fast printed-only parser (it is still reachable through the generic `openai-compatible` provider with no preset) |
| Mistral small | 280.7% handwritten CER | n/a |
| Grounded prompting of general VLMs for boxes | Unreliable geometry; `layout.Fill` covers the useful case | A model publishes layout benchmark results for grounding |

Ruled-out models stay *reachable* through the generic `openai-compatible`
provider. We just don't ship presets, prompts, self-hosting or docs for them.

## 5. Future work (from the proposal's out-of-scope list)

1. **Calibrate profile weights** against downstream CER on Scribe GT (needs
   ≥ 30 marked pages per profile).
2. **Run METATR through htr.** Add a converter from the Teklia ATR-benchmark
   format to an `htr eval` CSV, then score Gemini 4 and the current providers.
   This shows whether METATR's ranking holds for current model versions. It
   also gives Scribe a public, reproducible transcription benchmark alongside
   its private GT.
3. **Table-structure metrics** (GriTS, TEDS) for `ledger` and `form` once Scribe
   stores cell structure.
4. **No-ground-truth layout metrics** (Beyene & Dancy, arXiv:2509.13236).
5. **Self-hosted model containers** behind the `remote` v2 contract, for
   candidates that pass the gate.
6. **Move Scribe's hOCR parser** into `ocrpage`.

## 6. Benchmarks to watch

- METATR (evolving benchmark; check for new model versions and the release of
  its evaluation code).
- OmniHandwritingOCR (arXiv:2608.18586): handwriting diagnostics for VLMs.
- ICDAR 2026 competition on multilingual medieval HTR (cited in
  arXiv:2609.20064).
- PP-DocLayoutV3 independent benchmarks (xberg-io/xberg#2284).
- The MOTe paper's library (`mote_error`), for cross-checking `segeval`.

## Sources

- METATR: https://arxiv.org/abs/2605.26712 · https://huggingface.co/datasets/Teklia/ATR-benchmark
- OmniDocBench: https://arxiv.org/abs/2412.07626
- PaddleOCR-VL-1.6: https://arxiv.org/pdf/2606.03264 · PaddleOCR-VL-1.5: https://arxiv.org/pdf/2601.21957
- Chandra OCR 2: https://huggingface.co/datalab-to/chandra-ocr-2
- OXR benchmark: https://github.com/SoMarkAI/OXR/blob/main/docs/benchmark.md
- Leaderboard roundups (secondary): https://www.docsumo.com/blog/best-ocr-models · https://benchmarklist.com/benchmarks/olmocr_bench/ · https://interfaze.ai/leaderboards/olmocr
- Jina OCR v1: https://jina.ai/news/jina-ocr-v1-faster-document-parsing-on-low-budget-gpus/
- IAM cloud OCR comparison: https://arxiv.org/abs/2502.20295
- AIMultiple handwriting: https://aimultiple.com/handwriting-recognition
- Handwritten forms: https://arxiv.org/abs/2604.16504
- Transkribus claim (secondary): https://www.llamaindex.ai/insights/best-ai-for-handwriting-recognition
- PP-DocLayout: https://arxiv.org/abs/2503.17213 · https://github.com/xberg-io/xberg/issues/2284
- DocLayout-YOLO: https://github.com/opendatalab/DocLayout-YOLO
- BLLA anecdote: https://github.com/thodel/agentic_historian/pull/707
- Laypa: https://dl.acm.org/doi/fullHtml/10.1145/3604951.3605520
- OmniHandwritingOCR: https://arxiv.org/abs/2608.18586
