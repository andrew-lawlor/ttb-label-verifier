<img src="internal/webassets/web/static/logo.png" alt="" width="80" height="80">

# TTB Label Verification Prototype

A small tool that checks an alcohol label photo against the application
data on file — brand name, class/type, alcohol content, net contents, and
government warning — and reports what matches, what's off, and what needs a
human to look at it. Built for the take-home described in
[treasurytakehome-rgb/instructions](https://github.com/treasurytakehome-rgb/instructions).

**Live deployment:** https://ttb-poc.lawlor.io

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
- **Government warning is checked against a fixed constant, not typed in
  by the agent.** It's federally mandated text (27 CFR § 16.21), identical
  for every product — there's no real "application says X" for it the way
  there is for the other four fields. The single-label page shows it
  read-only instead of an editable textarea, and the batch CSV manifest
  has no column for it. This also closes a real failure mode the editable
  version had: a mistyped or stale-pasted warning would fail a label that
  actually complied.
- **Batch uploads run as an async job with a bounded worker pool**
  (15 concurrent extraction calls), with htmx polling the job for progress
  — 200-300 labels finish in a couple of minutes instead of ~25 minutes
  processed one at a time.
- **Optional application-PDF upload pre-fills Brand Name** on the
  single-label page. Not in the brief — added after inspecting the real
  [TTB Form 5100.31](https://www.ttb.gov/system/files/images/pdfs/forms/f510031.pdf),
  which turns out to have no field at all for class/type, alcohol content,
  net contents, or the government warning — only Brand Name exists as
  independent application data; everything else lives only on the
  physically-affixed label. See `SPEC.md` §6 for the full finding and why
  the feature is scoped to just that one field.

Assumptions and trade-offs (also covered in `SPEC.md` §2-4):

- No COLA integration — this is a standalone prototype, as the brief
  describes.
- Default extraction (OCR) has no contextual judgment beyond a layout
  heuristic — see `SPEC.md` §5 for exactly what it assumes about label
  layout and where that could break. The Claude vision alternative is kept
  working precisely so a real production conversation isn't starting from
  scratch.
- **No persistent database — batch results live in memory for the life of
  the process.** This is consistent with "we're not storing anything
  sensitive for this exercise," but has a real consequence worth stating
  plainly: a batch job's progress and results do not survive a process
  restart, and this deployment assumes a single instance (there's no shared
  store, so a second instance polling `GET /api/verify/batch/{id}` for a
  batch submitted to the first instance would get a 404). Fine for a
  single-instance prototype; a real production deployment behind a
  load balancer or on a platform that restarts/rescales instances would
  need a shared store (e.g. Postgres or Redis) behind that endpoint —
  not SQLite, which is itself single-file/single-writer and wouldn't
  actually solve the multi-instance case either.
- Poor-quality/skewed/glare-y photos are a known weak point of the default
  OCR backend (confirmed against `testdata/labels/09_low_quality_blurry.jpg`,
  which fails extraction rather than reading through the noise) — called
  out in the brief itself as possibly out of scope, and the documented
  reason to reach for `EXTRACTION_BACKEND=claude` if this matters for a
  given deployment.

## Setup

Requires Go 1.24+, Tesseract (default OCR backend), and Poppler (the
optional application-PDF brand-name pre-fill — the rest of the app works
fine without it, that one feature just disables itself):

```bash
# Debian/Ubuntu
sudo apt-get install -y tesseract-ocr poppler-utils

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

## Application PDF (TTB Form 5100.31)

On the single-label page, an optional PDF upload extracts just the Brand
Name field (Item 6) and pre-fills the form for review — the one field the
real form actually provides as independent application data. See
`SPEC.md` §6 for why the other four fields aren't attempted from the PDF.
`testdata/ttb-form/f510031.pdf` is the real blank form, used by
`internal/extract/pdfform_test.go` to confirm the crop region doesn't pick
up stray text from the adjacent field labels.

## Batch manifest format

The batch page needs a CSV alongside the label images, mapping each
image's filename to its application data. No `government_warning` column —
every label is checked against the one federally-required text
automatically (see "Approach, in brief" above). A downloadable template
(`internal/webassets/web/static/manifest-template.csv`, linked from the
batch page) opens fine in Excel — it stays a valid CSV as long as it's
edited and saved in place, not "Save As"'d to a different format:

```csv
filename,brand_name,class_type,alcohol_content,net_contents
label-1.jpg,Old Tom Distillery,Kentucky Straight Bourbon Whiskey,45% Alc./Vol.,750 mL
```

## Deployment

Deployed via Docker Compose on a Debian VPS: an `app` service (this repo's
`Dockerfile` — Go binary plus the two runtime dependencies it shells out
to, `tesseract-ocr` and `poppler-utils`, neither statically linked) behind
a `caddy` service that terminates TLS automatically (Let's Encrypt) for a
subdomain pointed at the VPS. Caddy is a custom build
(`Caddy.Dockerfile`) with a rate-limit plugin capping the OCR/PDF
endpoints at 10 requests/minute per IP — a public unauthenticated URL is
an open-ended abuse surface, same reasoning as defaulting to local OCR
over a paid API in the first place. See [`DEPLOY.md`](./DEPLOY.md) for the
exact runbook. No secrets required in the default configuration; set
`EXTRACTION_BACKEND=claude` and `ANTHROPIC_API_KEY` as real secrets
(not committed) only if switching backends.

The same `Dockerfile` works unchanged on Fly.io, Cloud Run, or any other
container platform, if a managed-TLS/managed-supervision platform is
preferred over VPS ops — see `SPEC.md` §10 for that trade-off.

```bash
docker compose up -d --build
```
