// OCRClient implements Extract via the local `tesseract` CLI instead of a
// hosted vision API — no network call, no per-request cost, no outbound
// dependency (see SPEC.md §4 and README "Extraction backend" for why this
// is the default: the app is deployed on the public internet, so a paid
// API behind it is a real abuse/cost vector, not just a nice-to-have to
// avoid).
//
// Tesseract only reads text; it has no notion of which line is the brand
// name vs. the class/type designation. This file adds that structure with
// a layout heuristic tuned to how TTB labels are conventionally laid out:
// the government warning is found by anchoring on its required phrase, ABV
// and net contents are found by pattern (they're the only fields with a
// distinctive unit token), and — this is the one genuinely fragile
// assumption — the brand name is taken to be the largest text near the top
// of the label, with the class/type designation the next-largest. That
// holds for every label in testdata/labels/ and for conventional label
// design generally, but an unusual layout (small brand text, oversized
// producer address, etc.) could fool it. Flagged as a known limitation in
// the README rather than silently assumed to always work.
package extract

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

// heightSimilarityRatio controls how close two lines' average glyph height
// must be to count as "the same size" — lets a brand name that wraps
// across two lines (e.g. "OLD TOM" / "DISTILLERY") be treated as one field
// instead of splitting brand from class/type mid-name.
const heightSimilarityRatio = 0.85

type OCRClient struct {
	binary string
}

// NewOCR constructs a client that shells out to the `tesseract` binary on
// PATH. Requires the tesseract-ocr package (with English trained data) to
// be installed on the host.
func NewOCR() *OCRClient {
	return &OCRClient{binary: "tesseract"}
}

func (c *OCRClient) Extract(ctx context.Context, imageBytes []byte, mediaType string) (model.ExtractedFields, error) {
	tmpPath, err := writeTempImage(imageBytes, mediaType)
	if err != nil {
		return model.ExtractedFields{}, err
	}
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, c.binary, tmpPath, "stdout", "tsv", "--psm", "6")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return model.ExtractedFields{}, fmt.Errorf("tesseract failed: %w (stderr: %s)", err, string(ee.Stderr))
		}
		return model.ExtractedFields{}, fmt.Errorf("tesseract failed: %w", err)
	}

	lines := parseTSVLines(string(out))
	if len(lines) == 0 {
		return model.ExtractedFields{}, ErrUnreadableImage
	}

	return fieldsFromLines(lines), nil
}

func writeTempImage(data []byte, mediaType string) (string, error) {
	ext := ".jpg"
	switch mediaType {
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	}
	f, err := os.CreateTemp("", "ttb-label-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file for ocr: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write temp file for ocr: %w", err)
	}
	return f.Name(), nil
}

// ocrLine is one reconstructed line of text from tesseract's word-level
// TSV output, with the position/size information the field heuristic needs.
type ocrLine struct {
	Text      string
	AvgHeight float64
	AvgConf   float64 // 0-1
	Top       int
}

// parseTSVLines reconstructs lines from tesseract's `tsv` output format:
// tab-separated columns
// level,page_num,block_num,par_num,line_num,word_num,left,top,width,height,conf,text
// grouped by (block,par,line), in top-to-bottom reading order.
func parseTSVLines(tsv string) []ocrLine {
	type key struct{ block, par, line int }
	type acc struct {
		words     []string
		heightSum float64
		confSum   float64
		count     int
		top       int
		hasTop    bool
	}

	groups := map[key]*acc{}
	var order []key

	scanner := bufio.NewScanner(strings.NewReader(tsv))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	skippedHeader := false
	for scanner.Scan() {
		row := scanner.Text()
		if !skippedHeader {
			skippedHeader = true
			continue
		}
		cols := strings.Split(row, "\t")
		if len(cols) < 12 {
			continue
		}
		level, _ := strconv.Atoi(cols[0])
		if level != 5 { // word-level rows only
			continue
		}
		text := strings.TrimSpace(cols[11])
		if text == "" {
			continue
		}
		block, _ := strconv.Atoi(cols[2])
		par, _ := strconv.Atoi(cols[3])
		line, _ := strconv.Atoi(cols[4])
		top, _ := strconv.Atoi(cols[7])
		height, _ := strconv.Atoi(cols[9])
		conf, _ := strconv.ParseFloat(cols[10], 64)
		if conf < 0 {
			conf = 0
		}

		k := key{block, par, line}
		a, ok := groups[k]
		if !ok {
			a = &acc{}
			groups[k] = a
			order = append(order, k)
		}
		a.words = append(a.words, text)
		a.heightSum += float64(height)
		a.confSum += conf
		a.count++
		if !a.hasTop {
			a.top, a.hasTop = top, true
		}
	}

	lines := make([]ocrLine, 0, len(order))
	for _, k := range order {
		a := groups[k]
		if a.count == 0 {
			continue
		}
		lines = append(lines, ocrLine{
			Text:      strings.Join(a.words, " "),
			AvgHeight: a.heightSum / float64(a.count),
			AvgConf:   a.confSum / float64(a.count) / 100.0,
			Top:       a.top,
		})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Top < lines[j].Top })
	return lines
}

var (
	warningAnchorRe = regexp.MustCompile(`(?i)government\s+warning`)
	abvLineRe       = regexp.MustCompile(`(?i)(\d+(\.\d+)?\s*%|\bproof\b)`)
	volumeLineRe    = regexp.MustCompile(`(?i)\d+(\.\d+)?\s*(ml|l\b|fl\.?\s*oz|oz\b)`)
)

// fieldsFromLines applies the layout heuristic described in this file's
// package comment to turn an unstructured list of OCR'd lines into the
// five fields the matching engine expects.
func fieldsFromLines(lines []ocrLine) model.ExtractedFields {
	used := make([]bool, len(lines))

	// Government warning: anchor on the required phrase, then take
	// everything from there to the end of the detected text as the
	// warning block (wrapped warning text spans several lines).
	warning := model.FieldExtraction{}
	warnIdx := -1
	for i, l := range lines {
		if warningAnchorRe.MatchString(l.Text) {
			warnIdx = i
			break
		}
	}
	if warnIdx >= 0 {
		var texts []string
		var confSum float64
		for i := warnIdx; i < len(lines); i++ {
			texts = append(texts, lines[i].Text)
			confSum += lines[i].AvgConf
			used[i] = true
		}
		warning = model.FieldExtraction{
			Value:      strings.Join(texts, " "),
			Confidence: confSum / float64(len(texts)),
		}
	}

	// Alcohol content: first remaining line with a "%" or "proof" token.
	abv := model.FieldExtraction{}
	for i, l := range lines {
		if used[i] {
			continue
		}
		if abvLineRe.MatchString(l.Text) {
			abv = model.FieldExtraction{Value: l.Text, Confidence: l.AvgConf}
			used[i] = true
			break
		}
	}

	// Net contents: first remaining line with a volume-unit token.
	netContents := model.FieldExtraction{}
	for i, l := range lines {
		if used[i] {
			continue
		}
		if volumeLineRe.MatchString(l.Text) {
			netContents = model.FieldExtraction{Value: l.Text, Confidence: l.AvgConf}
			used[i] = true
			break
		}
	}

	// Everything else above the first claimed line is the "header" —
	// brand name and class/type live here, by convention above the ABV/
	// net-contents block on a real label.
	headerEnd := len(lines)
	for i := 0; i < len(lines); i++ {
		if used[i] {
			headerEnd = i
			break
		}
	}
	var header []int
	for i := 0; i < headerEnd; i++ {
		if !used[i] {
			header = append(header, i)
		}
	}

	brand := extractTallestGroup(lines, &header)
	classType := extractTallestGroup(lines, &header)

	return model.ExtractedFields{
		BrandName:         brand,
		ClassType:         classType,
		AlcoholContent:    abv,
		NetContents:       netContents,
		GovernmentWarning: warning,
	}
}

// extractTallestGroup pulls the tallest remaining line(s) out of the
// header candidate set — including any near-equal-height neighbors, so a
// brand name wrapped across two lines is captured as one field — and
// returns them concatenated in reading order. Consumes the matched indices
// from header so a second call picks the next-tallest group.
func extractTallestGroup(lines []ocrLine, header *[]int) model.FieldExtraction {
	if len(*header) == 0 {
		return model.FieldExtraction{}
	}

	maxHeight := 0.0
	for _, idx := range *header {
		if lines[idx].AvgHeight > maxHeight {
			maxHeight = lines[idx].AvgHeight
		}
	}

	var matched, remaining []int
	for _, idx := range *header {
		if lines[idx].AvgHeight >= maxHeight*heightSimilarityRatio {
			matched = append(matched, idx)
		} else {
			remaining = append(remaining, idx)
		}
	}
	*header = remaining

	sort.Ints(matched) // restore reading order (original line index order)
	var texts []string
	var confSum float64
	for _, idx := range matched {
		texts = append(texts, lines[idx].Text)
		confSum += lines[idx].AvgConf
	}
	return model.FieldExtraction{
		Value:      strings.Join(texts, " "),
		Confidence: confSum / float64(len(matched)),
	}
}
