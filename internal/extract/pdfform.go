// PDFFormClient pulls the Brand Name field off a scanned/completed TTB
// Form 5100.31 (the real COLA application form — see
// https://www.ttb.gov/system/files/images/pdfs/forms/f510031.pdf) so an
// agent doesn't have to retype it.
//
// Brand Name is the *only* field extracted from the form, deliberately.
// Inspecting the real form's layout shows it has no field at all for
// class/type, alcohol content, net contents, or the government warning —
// those only exist on the physical label the applicant affixes to the
// form, which is exactly what the separate label-image upload is for.
// Auto-filling only Brand Name reflects what the form actually contains,
// rather than pretending it has application-side data it doesn't. See
// SPEC.md for the full explanation.
//
// The form also instructs applicants to print, sign in ink, and mail it
// in duplicate — real submissions are far more likely to be scans of a
// filled paper form than a still-fillable PDF with live form field
// values, so this reads the rendered page like an image (crop + OCR),
// the same approach as label extraction, rather than trying to read
// AcroForm field values that a scan wouldn't have anyway.
package extract

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

// Crop box for the Brand Name field (Item 6), derived from the form's own
// PDF text coordinates (see testdata/ttb-form-notes.md): the writable line
// runs from just below the "6. BRAND NAME (Required)" label (y=217pt) to
// just above the next field's label, "7. FANCIFUL NAME" (y=230pt), and
// spans the left-hand column width (x=14 to x=245pt) on the US-Letter
// (612x1008pt) page. Scaled to pixels at brandNameCropDPI.
const (
	brandNameCropDPI = 300
	brandNameCropX   = 58
	brandNameCropY   = 904
	brandNameCropW   = 963
	brandNameCropH   = 54
)

type PDFFormClient struct {
	pdftoppmBinary string
	tesseract      *OCRClient
}

func NewPDFForm() *PDFFormClient {
	return &PDFFormClient{
		pdftoppmBinary: "pdftoppm",
		tesseract:      NewOCR(),
	}
}

// ExtractBrandName returns the brand name read from Item 6 of a TTB F
// 5100.31 PDF. Returns a zero-confidence, empty-value result (not an
// error) if nothing could be read — this is meant to pre-fill a form
// field for a human to review, so a miss should degrade to "leave it
// blank," not fail the request.
func (c *PDFFormClient) ExtractBrandName(ctx context.Context, pdfBytes []byte) (model.FieldExtraction, error) {
	pdfPath, err := writeTempFile(pdfBytes, ".pdf")
	if err != nil {
		return model.FieldExtraction{}, err
	}
	defer os.Remove(pdfPath)

	outBase, err := os.MkdirTemp("", "ttb-pdfform-*")
	if err != nil {
		return model.FieldExtraction{}, fmt.Errorf("create temp dir for pdf crop: %w", err)
	}
	defer os.RemoveAll(outBase)
	outPrefix := outBase + "/crop"

	cmd := exec.CommandContext(ctx, c.pdftoppmBinary,
		"-png", "-r", fmt.Sprintf("%d", brandNameCropDPI),
		"-f", "1", "-l", "1",
		"-x", fmt.Sprintf("%d", brandNameCropX),
		"-y", fmt.Sprintf("%d", brandNameCropY),
		"-W", fmt.Sprintf("%d", brandNameCropW),
		"-H", fmt.Sprintf("%d", brandNameCropH),
		pdfPath, outPrefix,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return model.FieldExtraction{}, fmt.Errorf("pdftoppm failed: %w (%s)", err, string(out))
	}

	cropPath := outPrefix + "-1.png"
	lines, err := runTesseractTSV(ctx, c.tesseract.binary, cropPath, "7") // psm 7: single text line
	if err != nil {
		return model.FieldExtraction{}, err
	}
	if len(lines) == 0 {
		return model.FieldExtraction{}, nil // nothing legible — leave the form field blank, not an error
	}

	var texts []string
	var confSum float64
	for _, l := range lines {
		texts = append(texts, l.Text)
		confSum += l.AvgConf
	}
	return model.FieldExtraction{
		Value:      strings.TrimSpace(strings.Join(texts, " ")),
		Confidence: confSum / float64(len(lines)),
	}, nil
}

func writeTempFile(data []byte, ext string) (string, error) {
	f, err := os.CreateTemp("", "ttb-upload-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write temp file: %w", err)
	}
	return f.Name(), nil
}
