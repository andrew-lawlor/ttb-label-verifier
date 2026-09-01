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
- **One Claude vision API call per label** extracts fields with a
  structured tool-call schema (not free-form text parsing) — this is what
  keeps single-label verification within the ~5 second budget the brief
  calls out as a hard requirement (a prior vendor pilot failed here).
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
- Uses a hosted vision LLM (Claude). The brief mentions a past production
  firewall issue with a vendor's cloud ML endpoints; that's described as a
  production-environment problem from an earlier pilot, not a constraint on
  this prototype, which is explicitly framed as a standalone POC. A real
  procurement path would need to confirm this before moving past prototype.
- No persistent database — batch results live in memory for the life of
  the process, consistent with "we're not storing anything sensitive for
  this exercise."
- Poor-quality/skewed/glare-y photos are handled as best-effort by the
  vision model's own judgment, not specially pre-processed — called out in
  the brief itself as possibly out of scope.

## Setup

Requires Go 1.24+ and an Anthropic API key.

```bash
cp .env.example .env
# edit .env and set ANTHROPIC_API_KEY
```

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

Single static binary, no external runtime dependencies beyond network
access to `api.anthropic.com`. Containerize and deploy to any platform that
runs a Go binary (Fly.io, Cloud Run, Render, etc.); set `ANTHROPIC_API_KEY`
as a platform secret.

```bash
go build -o server ./cmd/server
```
