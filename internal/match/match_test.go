package match

import (
	"testing"

	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

func ext(v string) model.FieldExtraction {
	return model.FieldExtraction{Value: v, Confidence: 0.95}
}

// TestBrandNameCasingIsNotAFail is Dave's exact example from the
// interview notes: "STONE'S THROW" on the label vs "Stone's Throw" on the
// application should pass, not fail on literal string inequality.
func TestBrandNameCasingIsNotAFail(t *testing.T) {
	r := fuzzyField("brand_name", "Stone's Throw", ext("STONE'S THROW"))
	if r.Verdict != model.VerdictPass {
		t.Fatalf("expected pass, got %s (similarity %.2f)", r.Verdict, r.Similarity)
	}
}

func TestBrandNameBigDifferenceFails(t *testing.T) {
	r := fuzzyField("brand_name", "Old Tom Distillery", ext("Completely Different Co"))
	if r.Verdict != model.VerdictFail {
		t.Fatalf("expected fail, got %s (similarity %.2f)", r.Verdict, r.Similarity)
	}
}

// TestWarningTitleCaseFails is Jenny's exact example: "Government
// Warning" in title case instead of "GOVERNMENT WARNING" must be rejected,
// even though it's a near-identical string.
func TestWarningTitleCaseFails(t *testing.T) {
	app := "GOVERNMENT WARNING: (1) According to the Surgeon General..."
	label := "Government Warning: (1) According to the Surgeon General..."
	r := warningField(app, ext(label))
	if r.Verdict != model.VerdictFail {
		t.Fatalf("expected fail on case mismatch, got %s", r.Verdict)
	}
}

func TestWarningExactMatchPassesDespiteLineWrapDifferences(t *testing.T) {
	app := "GOVERNMENT WARNING: (1) According to the Surgeon General, women should not drink alcoholic beverages during pregnancy."
	label := "GOVERNMENT WARNING: (1) According to the Surgeon\nGeneral, women should   not drink alcoholic beverages during pregnancy."
	r := warningField(app, ext(label))
	if r.Verdict != model.VerdictPass {
		t.Fatalf("expected pass, got %s: %s", r.Verdict, r.Detail)
	}
}

func TestABVMatchesAcrossPercentAndProofNotation(t *testing.T) {
	r := abvField("45% Alc./Vol.", ext("90 Proof"))
	if r.Verdict != model.VerdictPass {
		t.Fatalf("expected pass (45%% == 90 proof), got %s: %s", r.Verdict, r.Detail)
	}
}

func TestABVOutsideToleranceFails(t *testing.T) {
	r := abvField("40% Alc./Vol.", ext("45% Alc./Vol."))
	if r.Verdict != model.VerdictFail {
		t.Fatalf("expected fail, got %s", r.Verdict)
	}
}

func TestNetContentsMatchesAcrossUnits(t *testing.T) {
	r := netContentsField("750 mL", ext("0.75 L"))
	if r.Verdict != model.VerdictPass {
		t.Fatalf("expected pass, got %s: %s", r.Verdict, r.Detail)
	}
}

func TestLowConfidenceExtractionForcesReview(t *testing.T) {
	r := fuzzyField("brand_name", "Old Tom Distillery", model.FieldExtraction{Value: "Old Tom Distillery", Confidence: 0.3})
	if r.Verdict != model.VerdictNeedsReview {
		t.Fatalf("expected needs_review due to low confidence, got %s", r.Verdict)
	}
}

func TestOverallVerdictIsWorstOfFields(t *testing.T) {
	app := model.ApplicationFields{
		BrandName:         "Old Tom Distillery",
		ClassType:         "Kentucky Straight Bourbon Whiskey",
		AlcoholContent:    "45% Alc./Vol.",
		NetContents:       "750 mL",
		GovernmentWarning: "GOVERNMENT WARNING: text",
	}
	extracted := model.ExtractedFields{
		BrandName:         ext("Old Tom Distillery"),
		ClassType:         ext("Kentucky Straight Bourbon Whiskey"),
		AlcoholContent:    ext("45% Alc./Vol."),
		NetContents:       ext("750 mL"),
		GovernmentWarning: ext("Government Warning: text"), // case mismatch -> fail
	}
	res := Result("1", "label.jpg", app, extracted)
	if res.OverallVerdict != model.VerdictFail {
		t.Fatalf("expected overall fail due to warning mismatch, got %s", res.OverallVerdict)
	}
}
