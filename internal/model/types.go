// Package model holds the data types shared across extraction, matching,
// batch processing, and the HTTP layer.
package model

// ApplicationFields is what the agent submits — the data on file, i.e. what
// the label is supposed to say. Deliberately has no GovernmentWarning
// field: unlike the other four, that text isn't applicant-declared data —
// it's fixed by federal regulation (27 CFR § 16.21), identical across
// every product regardless of type. See match.CanonicalGovernmentWarning,
// which every label's warning is checked against directly.
type ApplicationFields struct {
	BrandName      string `json:"brand_name"`
	ClassType      string `json:"class_type"`
	AlcoholContent string `json:"alcohol_content"` // e.g. "45% Alc./Vol." or "90 Proof"
	NetContents    string `json:"net_contents"`    // e.g. "750 mL"
}

// ExtractedFields is what the vision model read off the label image.
// Confidence is the model's own self-reported confidence per field
// (0-1), used to route uncertain extractions into the "needs review" band
// alongside genuinely borderline fuzzy matches.
type ExtractedFields struct {
	BrandName         FieldExtraction `json:"brand_name"`
	ClassType         FieldExtraction `json:"class_type"`
	AlcoholContent    FieldExtraction `json:"alcohol_content"`
	NetContents       FieldExtraction `json:"net_contents"`
	GovernmentWarning FieldExtraction `json:"government_warning"`
}

type FieldExtraction struct {
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
}

// Verdict is the outcome of comparing one field's extracted value against
// the submitted application value.
type Verdict string

const (
	VerdictPass        Verdict = "pass"
	VerdictNeedsReview Verdict = "needs_review"
	VerdictFail        Verdict = "fail"
)

// FieldResult is the per-field comparison outcome shown to the agent —
// always includes both raw values so the agent can see why, not just a
// boolean.
type FieldResult struct {
	Field            string  `json:"field"`
	ApplicationValue string  `json:"application_value"`
	ExtractedValue   string  `json:"extracted_value"`
	Verdict          Verdict `json:"verdict"`
	Similarity       float64 `json:"similarity,omitempty"` // fuzzy fields only
	Detail           string  `json:"detail,omitempty"`     // human-readable reason, esp. for fail
}

// VerifyResult is the full result for one label.
type VerifyResult struct {
	ID             string        `json:"id"`
	Filename       string        `json:"filename,omitempty"`
	OverallVerdict Verdict       `json:"overall_verdict"`
	Fields         []FieldResult `json:"fields"`
	Error          string        `json:"error,omitempty"` // set if extraction itself failed (unreadable image, etc.)
}

// BatchStatus tracks progress of an async batch job.
type BatchStatus struct {
	ID        string         `json:"id"`
	Total     int            `json:"total"`
	Completed int            `json:"completed"`
	Done      bool           `json:"done"`
	Results   []VerifyResult `json:"results"`
}
