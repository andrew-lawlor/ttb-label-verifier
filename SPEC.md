# TTB Label Verification Prototype — Spec

Source: [treasurytakehome-rgb/instructions](https://github.com/treasurytakehome-rgb/instructions)

## 1. Problem

TTB compliance agents manually compare label artwork against application data
(brand name, class/type, ABV, net contents, government warning) — mostly
straightforward field matching, occasionally requiring human judgment (e.g.
"STONE'S THROW" vs "Stone's Throw" is fine; a reworded government warning is
not). Goal: a prototype that does the matching automatically, fast enough to
actually get used, and simple enough for the least technical agent on the
team.

## 2. Hard requirements (from stakeholder interviews)

| Requirement | Source | Design consequence |
|---|---|---|
| ~5s response time, single label | Sarah — prior vendor pilot failed at 30-40s/label | One vision-model call per label, structured-output extraction, no multi-step agent chaining, no second LLM call for the comparison itself |
| Batch upload, 200-300 labels at once | Sarah — Janet/Seattle office | Async batch job + worker-pool concurrency, polling/progress UI, not a synchronous request |
| Usable by the least technical agent (73-y/o benchmark) | Sarah | Minimal-click web UI: drag-drop or select, one primary action, plain-language pass/fail, no configuration screens |
| Government warning must match **exactly** — wording, ALL CAPS, "GOVERNMENT WARNING:" prefix | Jenny | Warning field is byte/whitespace-normalized *exact* match, not fuzzy — the one field where "close enough" is a fail |
| Brand/class fields need judgment, not literal string equality | Dave | Normalized fuzzy match (case/punctuation/whitespace-insensitive, edit-distance threshold) with a "needs review" band instead of a hard boolean |
| No COLA integration; standalone POC | Marcus | No auth/integration work; app takes application data as direct input (form or batch file), not pulled from an external system |
| Prototype only — no real PII/sensitive storage | Marcus | No persistent database of real applications; batch results held in memory/temp storage for the session, not retained |

## 3. Explicitly out of scope

- COLA system integration.
- Production security/PII/document-retention hardening (noted as future work, not built).
- Robust handling of skewed/glare/poor-quality photos (Jenny flagged this as "maybe out of scope" — we treat it as a stretch goal, not core).
- Multi-user auth, roles, audit trail.

## 4. One deliberate assumption worth flagging

Marcus's note about the firewall blocking outbound calls to cloud ML
endpoints describes their **production** Azure environment during a past
vendor pilot — not a constraint on this prototype, which he separately
describes as a standalone POC ("just don't do anything crazy... we're not
storing anything sensitive for this exercise"). This build uses a hosted
multimodal LLM (Claude vision) for extraction. That's called out explicitly
in the README as a trade-off: a real procurement path would need to confirm
whether an on-prem/FedRAMP-authorized model is required before this
approach could move past prototype.

## 5. Architecture

```
┌─────────────────┐      ┌──────────────────────┐      ┌─────────────────┐
│  Web UI (single  │─────▶│  Go API server        │─────▶│  Claude vision   │
│  static page,    │      │  - /verify (single)   │      │  API (structured │
│  vanilla JS)      │◀─────│  - /verify/batch      │◀─────│  JSON extraction)│
└─────────────────┘      │  - /verify/batch/:id   │      └─────────────────┘
                          │  worker pool (goroutines,│
                          │  bounded concurrency)    │
                          └──────────────────────┘
```

- **Backend**: Go, single static binary. stdlib `net/http` (or `chi`) is
  enough — no need for a heavier framework at this scope.
- **Frontend**: server-rendered HTML via Go `html/template` + [htmx](https://htmx.org)
  (vendored locally, not CDN-loaded — consistent with treating outbound
  dependencies as a liability per Marcus's firewall story). No SPA
  framework, no JS build step. htmx's polling (`hx-trigger="every 2s"`)
  drives the batch-progress view directly against `GET /api/verify/batch/{id}`
  without any client-side state management — a natural fit for the "obvious,
  no hunting for buttons" requirement.
- **Extraction, default: local OCR (Tesseract), not a cloud vision API.**
  This is a revision from the original design — worth stating plainly since
  it's a real architectural pivot, not a footnote. Reasoning:
  1. **This app is deployed at a public URL for evaluation.** A paid vision
     API sitting behind a public endpoint with no auth is an open-ended
     cost/abuse vector — anyone who finds the URL can run up charges, not
     just the intended evaluators. OCR has zero marginal cost per request.
  2. It also directly answers Marcus's firewall story: no outbound
     dependency at all, not even a documented assumption to defend later.
  3. It deterministically fits the ~5s budget rather than being subject to
     a third party's latency that day.
  4. Tesseract reads text; it doesn't understand label semantics, so a
     small layout heuristic (see `internal/extract/ocr.go`) maps OCR'd
     lines to fields: the government warning is found by anchoring on its
     required phrase, ABV/net-contents by their distinctive unit tokens,
     and — the one genuinely fragile assumption — brand name is taken as
     the largest text near the top of the label, class/type the
     next-largest. Holds for every image in `testdata/labels/`, and for
     conventional label design generally, but is a known limitation on an
     unconventional layout.
  5. Traded off: OCR has no contextual judgment beyond that heuristic and
     is meaningfully weaker on poor-quality photos (confirmed against
     `testdata/labels/09_low_quality_blurry.jpg` — the deliberately
     degraded fixture fails extraction rather than reading through the
     noise a vision LLM might manage). Verified against all 11 test
     fixtures: 10/10 non-degraded cases produce the exact expected verdict.

  **Claude vision is still implemented** (`internal/extract/extract.go`)
  and works — one API call per label, tool-use/structured output forcing a
  JSON shape (`brand_name`, `class_type`, `alcohol_content`, `net_contents`,
  `government_warning`, `confidence` per field). It's the more robust
  choice for a real production deployment behind auth/rate-limiting, where
  the cost-exposure argument above no longer applies. Both backends
  implement the same `Extractor` interface, so swapping is one env var
  (`EXTRACTION_BACKEND=claude`), not a rewrite.
- **Matching engine**: plain Go code, not an LLM call — the extraction is
  the only place an LLM is used. Comparison is deterministic, fast, and
  auditable (agents can see exactly why something failed).

## 6. Application-PDF ingestion (TTB Form 5100.31)

Added after inspecting the real TTB application form
([F 5100.31](https://www.ttb.gov/system/files/images/pdfs/forms/f510031.pdf)),
which the take-home brief itself never provides — this was independent
research, not something the exercise handed us. Worth stating plainly what
that inspection found, because it reframes what "extract application data
from the PDF" can actually mean:

- **The real form has no field for class/type, alcohol content, net
  contents, or the government warning.** Item 5 is a coarse checkbox (Wine
  / Distilled Spirits / Malt Beverages), not a class/type designation. The
  rest of the page is a big empty box: "AFFIX COMPLETE SET OF LABELS
  BELOW" — TTB reads those four fields straight off the physically-affixed
  label, the same object our separate label-image upload already covers.
- **Item 6, Brand Name, is the one field that genuinely exists on the form
  as independent application data** — the only field with two real
  sources to compare, which is what the brief's fictional Dave-interview
  example ("STONE'S THROW" on the label vs. "Stone's Throw" on the
  application) is actually simplifying.
- **The form instructs applicants to print, sign in ink, and mail it in
  duplicate.** A submitted PDF is far more likely to be a scan of a filled
  paper form than a still-fillable PDF with live AcroForm field values —
  so this reads the rendered page like an image (crop + OCR), the same
  approach as label extraction, rather than trying to read form-field
  values a scan wouldn't have.

**What's built:** an optional PDF upload on the single-label page
(`internal/extract/pdfform.go`) that rasterizes page 1 (`pdftoppm`), crops
a fixed pixel region around Item 6 (coordinates derived from the form's own
PDF text positions — see the constants and comments in that file), and OCRs
just that crop to pre-fill the Brand Name field. The agent still reviews
and can edit it before submitting — same "never silently trusted"
principle as every other extracted value in this app. It's additive, not a
replacement: the label-image upload stays required, since that's still the
only real source for the other four fields.

**Deliberately not built:** auto-locating and cropping the physically
affixed label out of the PDF as an alternative to the direct photo upload.
Glued-label placement and size vary per real submission — a much less
reliable computer-vision problem than what the brief actually asks for,
and out of scope for this exercise's time-box.

**Known limitation:** the crop coordinates are hardcoded against the
04/2023 revision of this specific form; a different revision or a
meaningfully skewed scan could shift the field out of the cropped region.
Validated against `testdata/ttb-form/f510031.pdf` (the real blank form —
`TestExtractBrandNameFromBlankForm` confirms the crop doesn't bleed into
adjacent label text) and, since no real filled submission was available to
test against, against a synthetic single-page PDF with known text placed
at the same field coordinates (see commit history / test comments for that
account) — both read back correctly.

## 7. Field matching rules

| Field | Match type | Notes |
|---|---|---|
| Brand name | Fuzzy | Normalize case/whitespace/punctuation, then Levenshtein ratio ≥ threshold → pass; mid-range → "needs review"; low → fail |
| Class/type | Fuzzy | Same normalization as brand name |
| Alcohol content | Numeric, tolerance | Parse `%` / proof, compare numerically, small tolerance for rounding (e.g. 45% vs 45.0%) |
| Net contents | Normalized exact | Normalize units (mL/L/oz) to one unit, then exact compare |
| Government warning | Exact, normalized whitespace only, against a fixed constant | Case-sensitive, wording-sensitive; only trims stray whitespace/line breaks. Anything else is a hard fail — this is the one field Jenny called out as needing to be strict |

Every result includes the raw extracted value, the submitted value, the
verdict, and (for fuzzy fields) the similarity score — agents see *why*, not
just pass/fail, addressing Dave's "you need judgment" concern by keeping a
human in the loop for the "needs review" band rather than pretending the
tool is 100% authoritative.

**Government warning is checked differently from the other four**: it's
compared against `match.CanonicalGovernmentWarning`, a constant, not an
application-submitted value. Per §6's research into the real TTB form, this
text isn't applicant-declared data at all — it's fixed by federal
regulation (27 CFR § 16.21), word-for-word identical across every product
regardless of type. `model.ApplicationFields` has no `GovernmentWarning`
field as a result, the single-label page shows the required text read-only
instead of an editable textarea, and the batch manifest CSV has no
`government_warning` column. This is both more accurate to reality and
removes a real failure mode the editable-field version had: an agent
mistyping or pasting a stale copy of the warning would previously fail a
label that actually complied.

**Why class/type, alcohol content, and net contents weren't collapsed the
same way**, even though the real TTB form doesn't have independent fields
for those either (§6) — same situation as the warning: the brief's own
fictional interview notes never frame the warning as an "application says
X, label says Y" comparison, only as something that must be exact. Class/
type and ABV get the opposite treatment: Sarah says outright, *"Brand name
matches? Check. ABV is correct? Check,"* and Dave's whole anecdote is a
label-vs-application comparison. The brief is explicitly asking for a
matching tool on those three fields, even though the real form doesn't
actually support that model. Rebuilding them into "derive from the label,
validate against regulatory rules" instead — which the real-world research
would arguably support — would mean overriding the brief's explicit ask
based on research it never asked for, a materially bigger and riskier move
than the warning-field change. That one was justified because the brief
itself never pretended there was an application-side value to compare
there; these three don't have that same justification. Kept as manual
entry (plus the PDF pre-fill for brand name, §6) on purpose, not by
default.

## 8. API sketch

- `POST /api/verify` — multipart: one label image + application fields (JSON). Synchronous, returns verdict in ~5s.
- `POST /api/verify/batch` — multipart: N label images + a manifest (CSV/JSON mapping filename → application fields). Returns a `batch_id` immediately.
- `GET /api/verify/batch/{id}` — poll for progress (`completed/total`) and results as they land.
- `POST /api/extract-brand-name` — multipart: one application PDF. Returns the Brand Name form field pre-filled (or a "couldn't find it" note), for the agent to review before the actual `/api/verify` submission. Single-label page only — see §6.
- `GET /version` — the git commit SHA the running binary was built from (injected at build time via `-ldflags`, empty/`dev` for a plain local `go run`). Deploys here are manual (`git pull` + `docker compose up -d --build` on the VPS, no CI/CD), so this is how "is the server running the latest code" gets answered without SSHing in to compare by hand — see `DEPLOY.md` "Checking what's running."
- Batch worker pool: bounded concurrency (e.g. 10-20 in flight) against the vision API, so 200-300 labels finish in a couple of minutes rather than serially at ~5s each (~25 min).

## 9. Error handling

- Unreadable/corrupt image → explicit "could not read label" result, not a silent fail.
- Low-confidence extraction (model itself flags uncertainty) → surfaced as "needs review," same as the fuzzy mid-band.
- Partial batch failure → batch keeps going; failed items are reported individually, one bad file doesn't kill the run.

## 10. Deployment

- Deployed via Docker Compose on a Debian VPS (Hetzner) rather than a
  managed container platform (Fly.io/Cloud Run) — a deliberate choice, not
  a default. The app's own design already assumes a single always-on
  instance with in-memory batch state (§"No persistent database" in
  README), which a plain VPS satisfies by construction with nothing to
  configure; a managed platform's scale-to-zero behavior (Cloud Run's
  default, Render's free tier) would risk killing an in-flight batch and
  needs an explicit always-on setting to avoid. What a VPS gives up in
  exchange — managed TLS, managed process supervision, push-to-deploy — is
  covered by `caddy` (automatic Let's Encrypt) and Docker Compose's
  `restart: unless-stopped` in this repo's `docker-compose.yml`.
- Container needs `tesseract-ocr` (label extraction) and `poppler-utils` (application-PDF brand-name pre-fill) installed alongside the Go binary — neither is statically linked in. Handled in `Dockerfile`.
- The same `Dockerfile` also works unchanged on Fly.io/Cloud Run/Render if a managed platform is preferred later — the VPS choice is about this specific deployment, not a constraint baked into the app itself.
- Anthropic API key via a real secret (`.env`, gitignored), never committed — only needed if `EXTRACTION_BACKEND=claude`.
- **Rate-limited at the Caddy layer**: `POST /api/*` (the OCR/PDF-extraction
  endpoints — real CPU cost per request) capped at 10 requests/minute per
  client IP, via a custom Caddy build with the `mholt/caddy-ratelimit`
  plugin (`Caddy.Dockerfile`). This is the same reasoning that drove
  defaulting to local OCR over a cloud API in the first place: a public,
  unauthenticated URL is an open-ended abuse surface, and CPU exhaustion
  from repeated OCR calls is a real vector even with no paid API to run up.
  Scoped by HTTP method so it doesn't catch `GET
  /api/verify/batch/{id}` — htmx polls that every 2s during a batch, and
  catching it in the same limit would break the progress UI.
- Deliverables: public GitHub repo + live deployed URL, per the brief. See `DEPLOY.md` for the exact runbook.

## 11. Stretch goals (only if core is solid and time remains)

- Basic image-quality pre-check (blur/glare) with a friendly re-upload prompt — Jenny's ask, explicitly optional.
- CSV export of batch results.
- Confidence visualization in the UI (color-coded pass/review/fail).

## 12. Non-goals reminder

Per the brief's own evaluation criteria: a complete, clean core beats an
ambitious but partial build. Batch + single-label verification with solid
matching rules and a deployed URL is the bar; stretch goals are truly
optional.
