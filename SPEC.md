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
- **Frontend**: one static HTML page + vanilla JS (or htmx). No SPA
  framework — reduces surface area and matches the "obvious, no hunting for
  buttons" requirement.
- **Extraction**: one Claude vision API call per label image, using tool-use
  / structured output to force a JSON shape (`brand_name`, `class_type`,
  `alcohol_content`, `net_contents`, `government_warning_text`,
  `confidence` per field).
- **Matching engine**: plain Go code, not an LLM call — the extraction is
  the only place an LLM is used. Comparison is deterministic, fast, and
  auditable (agents can see exactly why something failed).

## 6. Field matching rules

| Field | Match type | Notes |
|---|---|---|
| Brand name | Fuzzy | Normalize case/whitespace/punctuation, then Levenshtein ratio ≥ threshold → pass; mid-range → "needs review"; low → fail |
| Class/type | Fuzzy | Same normalization as brand name |
| Alcohol content | Numeric, tolerance | Parse `%` / proof, compare numerically, small tolerance for rounding (e.g. 45% vs 45.0%) |
| Net contents | Normalized exact | Normalize units (mL/L/oz) to one unit, then exact compare |
| Government warning | Exact, normalized whitespace only | Case-sensitive, wording-sensitive; only trims stray whitespace/line breaks. Anything else is a hard fail — this is the one field Jenny called out as needing to be strict |

Every result includes the raw extracted value, the submitted value, the
verdict, and (for fuzzy fields) the similarity score — agents see *why*, not
just pass/fail, addressing Dave's "you need judgment" concern by keeping a
human in the loop for the "needs review" band rather than pretending the
tool is 100% authoritative.

## 7. API sketch

- `POST /api/verify` — multipart: one label image + application fields (JSON). Synchronous, returns verdict in ~5s.
- `POST /api/verify/batch` — multipart: N label images + a manifest (CSV/JSON mapping filename → application fields). Returns a `batch_id` immediately.
- `GET /api/verify/batch/{id}` — poll for progress (`completed/total`) and results as they land.
- Batch worker pool: bounded concurrency (e.g. 10-20 in flight) against the vision API, so 200-300 labels finish in a couple of minutes rather than serially at ~5s each (~25 min).

## 8. Error handling

- Unreadable/corrupt image → explicit "could not read label" result, not a silent fail.
- Low-confidence extraction (model itself flags uncertainty) → surfaced as "needs review," same as the fuzzy mid-band.
- Partial batch failure → batch keeps going; failed items are reported individually, one bad file doesn't kill the run.

## 9. Deployment

- Containerized Go binary, deployed to Fly.io or Cloud Run (single small always-on or scale-to-zero instance is enough for a prototype).
- Anthropic API key via platform secret/env var, never committed.
- Deliverables: public GitHub repo + live deployed URL, per the brief.

## 10. Stretch goals (only if core is solid and time remains)

- Basic image-quality pre-check (blur/glare) with a friendly re-upload prompt — Jenny's ask, explicitly optional.
- CSV export of batch results.
- Confidence visualization in the UI (color-coded pass/review/fail).

## 11. Non-goals reminder

Per the brief's own evaluation criteria: a complete, clean core beats an
ambitious but partial build. Batch + single-label verification with solid
matching rules and a deployed URL is the bar; stretch goals are truly
optional.
