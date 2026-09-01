package extract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTSVLinesGroupsWordsIntoLinesInReadingOrder(t *testing.T) {
	tsv := strings.Join([]string{
		"level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext",
		"5\t1\t1\t1\t1\t1\t73\t113\t151\t49\t95.4\tOLD",
		"5\t1\t1\t1\t1\t2\t250\t113\t170\t49\t96.5\tTOM",
		"5\t1\t1\t1\t2\t1\t73\t185\t442\t49\t96.0\tDISTILLERY",
		"5\t1\t2\t1\t1\t1\t70\t400\t300\t30\t90.0\t45%",
		"2\t1\t1\t1\t0\t0\t34\t32\t833\t6\t-1\t ", // non-word row, should be skipped
	}, "\n")

	lines := parseTSVLines(tsv)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %+v", len(lines), lines)
	}
	if lines[0].Text != "OLD TOM" {
		t.Errorf("expected first line %q, got %q", "OLD TOM", lines[0].Text)
	}
	if lines[1].Text != "DISTILLERY" {
		t.Errorf("expected second line %q, got %q", "DISTILLERY", lines[1].Text)
	}
	if lines[0].AvgHeight != 49 {
		t.Errorf("expected avg height 49, got %v", lines[0].AvgHeight)
	}
	if lines[0].AvgConf < 0.95 || lines[0].AvgConf > 0.96 {
		t.Errorf("expected avg conf ~0.955, got %v", lines[0].AvgConf)
	}
}

func TestFieldsFromLinesBaselineCase(t *testing.T) {
	lines := []ocrLine{
		{Text: "OLD TOM", AvgHeight: 49, AvgConf: 0.95, Top: 113},
		{Text: "DISTILLERY", AvgHeight: 49, AvgConf: 0.96, Top: 185},
		{Text: "Kentucky Straight Bourbon Whiskey", AvgHeight: 34, AvgConf: 0.93, Top: 270},
		{Text: "45% Alc./Vol. (90 Proof)", AvgHeight: 30, AvgConf: 0.90, Top: 400},
		{Text: "750 mL", AvgHeight: 30, AvgConf: 0.91, Top: 450},
		{Text: "Distilled and Bottled by Example Producer, Louisville, KY", AvgHeight: 18, AvgConf: 0.88, Top: 500},
		{Text: "GOVERNMENT WARNING: (1) According to the Surgeon General...", AvgHeight: 20, AvgConf: 0.85, Top: 940},
	}

	f := fieldsFromLines(lines)

	if f.BrandName.Value != "OLD TOM DISTILLERY" {
		t.Errorf("expected brand 'OLD TOM DISTILLERY', got %q", f.BrandName.Value)
	}
	if f.ClassType.Value != "Kentucky Straight Bourbon Whiskey" {
		t.Errorf("expected class/type to be the bourbon designation, got %q", f.ClassType.Value)
	}
	if f.AlcoholContent.Value != "45% Alc./Vol. (90 Proof)" {
		t.Errorf("unexpected ABV: %q", f.AlcoholContent.Value)
	}
	if f.NetContents.Value != "750 mL" {
		t.Errorf("unexpected net contents: %q", f.NetContents.Value)
	}
	if !strings.HasPrefix(f.GovernmentWarning.Value, "GOVERNMENT WARNING:") {
		t.Errorf("unexpected warning: %q", f.GovernmentWarning.Value)
	}
}

func TestFieldsFromLinesMissingFieldYieldsZeroConfidence(t *testing.T) {
	lines := []ocrLine{
		{Text: "SOME BRAND", AvgHeight: 49, AvgConf: 0.9, Top: 100},
		{Text: "Vodka", AvgHeight: 30, AvgConf: 0.9, Top: 200},
		// no ABV line, no net contents line, no warning line present
	}
	f := fieldsFromLines(lines)
	if f.AlcoholContent.Value != "" || f.AlcoholContent.Confidence != 0 {
		t.Errorf("expected empty/zero-confidence ABV when absent, got %+v", f.AlcoholContent)
	}
	if f.GovernmentWarning.Value != "" || f.GovernmentWarning.Confidence != 0 {
		t.Errorf("expected empty/zero-confidence warning when absent, got %+v", f.GovernmentWarning)
	}
}

// TestExtractAgainstFixtures runs the real tesseract binary against the
// repo's synthetic test labels and does a loose sanity check (not exact
// string equality, since OCR isn't perfectly deterministic) that each
// field was found and roughly matches the known ground truth. Skips
// itself if tesseract isn't installed, so this doesn't break on a machine
// without it.
func TestExtractAgainstFixtures(t *testing.T) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("tesseract not installed, skipping OCR integration test")
	}

	fixturesDir, err := filepath.Abs("../../testdata/labels")
	if err != nil {
		t.Fatal(err)
	}
	imgPath := filepath.Join(fixturesDir, "01_perfect_match_bourbon.jpg")
	data, err := os.ReadFile(imgPath)
	if err != nil {
		t.Skipf("fixture not found at %s: %v", imgPath, err)
	}

	client := NewOCR()
	fields, err := client.Extract(context.Background(), data, "image/jpeg")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"brand_name", fields.BrandName.Value, "OLD TOM DISTILLERY"},
		{"class_type", fields.ClassType.Value, "Kentucky Straight Bourbon Whiskey"},
	}
	for _, c := range checks {
		if !strings.Contains(strings.ToUpper(c.got), strings.ToUpper(c.want)) {
			t.Errorf("%s: got %q, want it to contain %q", c.name, c.got, c.want)
		}
	}
	if !strings.Contains(fields.AlcoholContent.Value, "45") {
		t.Errorf("alcohol_content: got %q, want it to contain '45'", fields.AlcoholContent.Value)
	}
	if !strings.Contains(fields.NetContents.Value, "750") {
		t.Errorf("net_contents: got %q, want it to contain '750'", fields.NetContents.Value)
	}
	if !strings.Contains(strings.ToUpper(fields.GovernmentWarning.Value), "GOVERNMENT WARNING") {
		t.Errorf("government_warning: got %q, want it to contain 'GOVERNMENT WARNING'", fields.GovernmentWarning.Value)
	}
}
