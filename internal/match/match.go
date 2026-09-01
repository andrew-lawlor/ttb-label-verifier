// Package match implements the deterministic comparison between what an
// agent submitted on an application and what the vision model extracted
// from the label image. This is plain Go, not a model call — see SPEC.md
// section 5: the LLM's job ends at extraction, the LLM never adjudicates.
package match

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

const (
	fuzzyPassThreshold   = 0.90
	fuzzyReviewThreshold = 0.70
	lowConfidenceCutoff  = 0.50
	abvToleranceAbs      = 0.5  // percentage points
	netContentsTolerance = 0.02 // 2% relative tolerance, covers rounding across units
)

// CanonicalGovernmentWarning is the federally-mandated warning statement
// (27 CFR § 16.21) every alcoholic beverage label must carry verbatim,
// regardless of product type. Unlike the other four fields, this isn't
// applicant-declared data with an independent source to compare against —
// there's one legally correct text, and every label is checked against it
// directly rather than against a per-application value.
const CanonicalGovernmentWarning = "GOVERNMENT WARNING: (1) According to the Surgeon General, " +
	"women should not drink alcoholic beverages during pregnancy because of the risk of birth defects. " +
	"(2) Consumption of alcoholic beverages impairs your ability to drive a car or operate machinery, " +
	"and may cause health problems."

// Result compares one label's extracted fields against the submitted
// application fields and produces the full per-field breakdown plus an
// overall verdict (worst-of-fields).
func Result(id, filename string, app model.ApplicationFields, ext model.ExtractedFields) model.VerifyResult {
	fields := []model.FieldResult{
		fuzzyField("brand_name", app.BrandName, ext.BrandName),
		fuzzyField("class_type", app.ClassType, ext.ClassType),
		abvField(app.AlcoholContent, ext.AlcoholContent),
		netContentsField(app.NetContents, ext.NetContents),
		warningField(ext.GovernmentWarning),
	}

	overall := model.VerdictPass
	for _, f := range fields {
		if f.Verdict == model.VerdictFail {
			overall = model.VerdictFail
			break
		}
		if f.Verdict == model.VerdictNeedsReview {
			overall = model.VerdictNeedsReview
		}
	}

	return model.VerifyResult{
		ID:             id,
		Filename:       filename,
		OverallVerdict: overall,
		Fields:         fields,
	}
}

// lowConfidenceDetail distinguishes "extraction found nothing for this
// field" (confidence 0 — e.g. OCR found no line matching this field's
// pattern) from "extraction found something but isn't sure of it," since
// both route to needs_review but mean different things to the agent
// reading the result.
func lowConfidenceDetail(ext model.FieldExtraction) string {
	if ext.Value == "" {
		return "field not found in the label image — human check needed"
	}
	return "low extraction confidence"
}

var normalizeRe = regexp.MustCompile(`[^a-z0-9 ]+`)
var whitespaceRe = regexp.MustCompile(`\s+`)

func normalize(s string) string {
	s = strings.ToLower(s)
	s = normalizeRe.ReplaceAllString(s, " ")
	s = whitespaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// fuzzyField handles fields where minor wording/casing/punctuation
// differences are expected and acceptable (Dave's "STONE'S THROW" vs
// "Stone's Throw" case) but large deviations are still a fail.
func fuzzyField(name, appValue string, ext model.FieldExtraction) model.FieldResult {
	a, b := normalize(appValue), normalize(ext.Value)
	sim := similarity(a, b)

	verdict := model.VerdictFail
	detail := ""
	switch {
	case ext.Confidence < lowConfidenceCutoff:
		verdict = model.VerdictNeedsReview
		detail = lowConfidenceDetail(ext)
	case sim >= fuzzyPassThreshold:
		verdict = model.VerdictPass
	case sim >= fuzzyReviewThreshold:
		verdict = model.VerdictNeedsReview
		detail = "close but not exact — human check suggested"
	default:
		verdict = model.VerdictFail
		detail = "does not match application"
	}

	return model.FieldResult{
		Field:            name,
		ApplicationValue: appValue,
		ExtractedValue:   ext.Value,
		Verdict:          verdict,
		Similarity:       sim,
		Detail:           detail,
	}
}

// warningField is intentionally the strictest rule in the system: the
// government warning must match CanonicalGovernmentWarning word-for-word,
// including case. Only whitespace differences (line wrapping on the label)
// are normalized away. See SPEC.md §7 / Jenny's interview. Unlike the
// other four fields, there's no applicant-supplied value to compare
// against — see the ApplicationFields doc comment and
// CanonicalGovernmentWarning's.
func warningField(ext model.FieldExtraction) model.FieldResult {
	normSpace := func(s string) string {
		return strings.TrimSpace(whitespaceRe.ReplaceAllString(s, " "))
	}
	a, b := normSpace(CanonicalGovernmentWarning), normSpace(ext.Value)

	verdict := model.VerdictFail
	detail := ""
	switch {
	case ext.Confidence < lowConfidenceCutoff:
		verdict = model.VerdictNeedsReview
		detail = lowConfidenceDetail(ext)
	case a == b:
		verdict = model.VerdictPass
	default:
		verdict = model.VerdictFail
		detail = "government warning text does not match the required statement exactly (wording, case, or punctuation)"
	}

	return model.FieldResult{
		Field:            "government_warning",
		ApplicationValue: CanonicalGovernmentWarning,
		ExtractedValue:   ext.Value,
		Verdict:          verdict,
		Detail:           detail,
	}
}

var pctRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*%`)
var proofRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*proof`)

// abvField extracts a percentage from either "%" or "proof" notation
// (proof = 2x ABV in the US) and compares numerically within a small
// rounding tolerance, rather than requiring identical text.
func abvField(appValue string, ext model.FieldExtraction) model.FieldResult {
	a, aOK := parseABV(appValue)
	b, bOK := parseABV(ext.Value)

	verdict := model.VerdictFail
	detail := ""
	switch {
	case ext.Confidence < lowConfidenceCutoff:
		verdict = model.VerdictNeedsReview
		detail = lowConfidenceDetail(ext)
	case !aOK || !bOK:
		verdict = model.VerdictNeedsReview
		detail = "could not parse alcohol content from one or both values"
	case abs(a-b) <= abvToleranceAbs:
		verdict = model.VerdictPass
	default:
		verdict = model.VerdictFail
		detail = fmt.Sprintf("application states %.1f%%, label reads %.1f%%", a, b)
	}

	return model.FieldResult{
		Field:            "alcohol_content",
		ApplicationValue: appValue,
		ExtractedValue:   ext.Value,
		Verdict:          verdict,
		Detail:           detail,
	}
}

func parseABV(s string) (float64, bool) {
	s = strings.ToLower(s)
	if m := pctRe.FindStringSubmatch(s); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil
	}
	if m := proofRe.FindStringSubmatch(s); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, false
		}
		return v / 2, true // proof -> ABV%
	}
	return 0, false
}

var volumeRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(ml|l|oz|fl\.?\s*oz)`)

// netContentsField normalizes common volume units to mL before comparing,
// so "750 mL" vs "0.75 L" or "25.4 fl oz" is a pass rather than a false
// mismatch.
func netContentsField(appValue string, ext model.FieldExtraction) model.FieldResult {
	a, aOK := parseVolumeML(appValue)
	b, bOK := parseVolumeML(ext.Value)

	verdict := model.VerdictFail
	detail := ""
	switch {
	case ext.Confidence < lowConfidenceCutoff:
		verdict = model.VerdictNeedsReview
		detail = lowConfidenceDetail(ext)
	case !aOK || !bOK:
		verdict = model.VerdictNeedsReview
		detail = "could not parse net contents from one or both values"
	case relDiff(a, b) <= netContentsTolerance:
		verdict = model.VerdictPass
	default:
		verdict = model.VerdictFail
		detail = fmt.Sprintf("application states %.1f mL equivalent, label reads %.1f mL equivalent", a, b)
	}

	return model.FieldResult{
		Field:            "net_contents",
		ApplicationValue: appValue,
		ExtractedValue:   ext.Value,
		Verdict:          verdict,
		Detail:           detail,
	}
}

func parseVolumeML(s string) (float64, bool) {
	m := volumeRe.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ReplaceAll(m[2], " ", "") {
	case "ml":
		return v, true
	case "l":
		return v * 1000, true
	case "oz", "fl.oz", "floz":
		return v * 29.5735, true
	}
	return 0, false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func relDiff(a, b float64) float64 {
	if a == 0 && b == 0 {
		return 0
	}
	denom := a
	if denom == 0 {
		denom = b
	}
	return abs(a-b) / abs(denom)
}

// similarity returns a 0-1 score (1 = identical) based on Levenshtein
// distance normalized by the longer string's length.
func similarity(a, b string) float64 {
	if a == "" && b == "" {
		return 1
	}
	dist := levenshtein(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(dist)/float64(maxLen)
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
