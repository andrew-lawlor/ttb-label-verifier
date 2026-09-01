# TTB Label Verification Prototype

A small tool that checks an alcohol label photo against the application
data on file — brand name, class/type, alcohol content, net contents, and
government warning — and reports what matches, what's off, and what needs a
human to look at it. Built for the take-home described in
[treasurytakehome-rgb/instructions](https://github.com/treasurytakehome-rgb/instructions).

Full design rationale, including how each requirement in the brief maps to
a design decision, is in [`SPEC.md`](./SPEC.md).

## Approach, in brief

- **Go backend, server-rendered HTML + [htmx](https://htmx.org) frontend.**
  No SPA framework, no JS build step, single static binary to deploy.
- **Extraction defaults to local OCR (Tesseract), not a cloud vision API.**
  This app is deployed at a public URL for evaluation, and a paid API with
  no auth in front of it is an open-ended cost/abuse vector — anyone who
  finds the URL can run it up, not just the intended evaluators. OCR is
  free per request, has zero outbound dependency, and deterministically
  fits the ~5 second budget the brief calls out as a hard requirement (a
  prior vendor pilot failed on exactly this). Claude vision is still
  implemented and available via `EXTRACTION_BACKEND=claude` — more robust
  on poor-quality photos, the better choice behind real auth/rate-limiting,
  and a one-env-var swap since both backends implement the same interface.
  See `SPEC.md` §5 for the full reasoning and its trade-offs.
- **Comparison logic is plain Go, not another LLM call** — deterministic,
  fast, and the reasoning is visible to the agent (raw values + similarity
  score + explanation), not a black box.
- **Field-specific matching rules**, not one-size-fits-all string equality:
  brand/class names are fuzzy-matched (so "STONE'S THROW" vs "Stone's
  Throw" passes), alcohol content and net contents are parsed and compared
  numerically across notations/units, and the government warning is matched
  **exactly** (case, wording, punctuation) since that's the one field the
  brief's interview notes explicitly say must be strict.
- **Batch uploads run as an async job with a bounded worker pool**
  (15 concurrent extraction calls), with htmx polling the job for progress
  — 200-300 labels finish in a couple of minutes instead of ~25 minutes
  processed one at a time.

Assumptions and trade-offs (also covered in `SPEC.md` §2-4):

- No COLA integration — this is a standalone prototype, as the brief
  describes.
- Default extraction (OCR) has no contextual judgment beyond a layout
  heuristic — see `SPEC.md` §5 for exactly what it assumes about label
  layout and where that could break. The Claude vision alternative is kept
  working precisely so a real production conversation isn't starting from
  scratch.
- No persistent database — batch results live in memory for the life of
  the process, consistent with "we're not storing anything sensitive for
  this exercise."
- Poor-quality/skewed/glare-y photos are a known weak point of the default
  OCR backend (confirmed against `testdata/labels/09_low_quality_blurry.jpg`,
  which fails extraction rather than reading through the noise) — called
  out in the brief itself as possibly out of scope, and the documented
  reason to reach for `EXTRACTION_BACKEND=claude` if this matters for a
  given deployment.

## Setup

Requires Go 1.24+ and, for the default OCR backend, Tesseract:

```bash
# Debian/Ubuntu
sudo apt-get install -y tesseract-ocr

cp .env.example .env   # defaults to EXTRACTION_BACKEND=ocr, no API key needed
```

To use the Claude vision backend instead, set `EXTRACTION_BACKEND=claude`
and `ANTHROPIC_API_KEY` in `.env`.

## Run

```bash
export $(cat .env | xargs)
go run ./cmd/server
```

Then open http://localhost:8080 for single-label verification, or
http://localhost:8080/batch for batch upload.

## Test

```bash
go test ./...
go test -race ./...   # batch worker pool concurrency
```

## Test labels

`testdata/labels/` has 11 synthetic label images plus a matching
`manifest.csv`, generated with `testdata/generate_labels.py` (Pillow —
`pip install pillow`, then `python3 testdata/generate_labels.py`). They're
rendered rather than pulled from an external image generator so the
ground-truth text is exact and each case targets a specific rule or edge
case straight from the take-home's interview notes — brand-name casing
(Dave's "STONE'S THROW" example), a government-warning title-case rejection
(Jenny's example), ABV/proof-notation equivalence, net-contents unit
conversion, a deliberately mismatched label, and a blurry/angled photo. See
`testdata/labels/EXPECTATIONS.md` for the expected verdict on each one. Feed
the whole set through the batch upload page as a quick end-to-end check.

## Batch manifest format

The batch page needs a CSV alongside the label images, mapping each
image's filename to its application data:

```csv
filename,brand_name,class_type,alcohol_content,net_contents,government_warning
label-1.jpg,Old Tom Distillery,Kentucky Straight Bourbon Whiskey,45% Alc./Vol.,750 mL,GOVERNMENT WARNING: ...
```

## Deployment

Single static Go binary plus the `tesseract` runtime dependency (the
default OCR backend shells out to it — it's not statically linked in).
Containerize with `tesseract-ocr` installed in the image and deploy to any
platform that runs a container (Fly.io, Cloud Run, Render, etc.). No
secrets required in the default configuration; set `EXTRACTION_BACKEND=claude`
and `ANTHROPIC_API_KEY` as a platform secret only if switching backends.

```bash
go build -o server ./cmd/server
```
