package extract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestExtractBrandNameFromBlankForm runs the real crop pipeline against
// the unfilled TTB F 5100.31 fixture. Item 6 (Brand Name) is blank on this
// form, so the correct result is empty/no confident text — a genuinely
// meaningful assertion that the hardcoded crop box (see the constants in
// pdfform.go) targets the writable line itself and doesn't bleed into the
// "6. BRAND NAME (Required)" label above it or the "7. FANCIFUL NAME"
// label below it, either of which would show up here as spurious text.
//
// This does not test extraction of an actual filled-in value — there's no
// real completed submission to use as a fixture, and generating a
// synthetic one accurately (matching the real form's coordinate space)
// isn't worth the machinery it'd take. That direction was validated
// manually instead: rendering the real form, drawing a brand name onto
// the exact pixel region this crop box targets, and confirming the crop +
// OCR pipeline reads it back correctly. See SPEC.md for that account.
func TestExtractBrandNameFromBlankForm(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed, skipping PDF form integration test")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("tesseract not installed, skipping PDF form integration test")
	}

	fixturePath, err := filepath.Abs("../../testdata/ttb-form/f510031.pdf")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found at %s: %v", fixturePath, err)
	}

	client := NewPDFForm()
	result, err := client.ExtractBrandName(context.Background(), data)
	if err != nil {
		t.Fatalf("ExtractBrandName failed: %v", err)
	}
	if result.Value != "" {
		t.Errorf("expected empty brand name on the blank form (crop box may be "+
			"bleeding into an adjacent label), got %q (confidence %.2f)", result.Value, result.Confidence)
	}
}
