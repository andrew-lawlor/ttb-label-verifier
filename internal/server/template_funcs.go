package server

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

var funcMap = template.FuncMap{
	"verdictClass":    verdictClass,
	"verdictLabel":    verdictLabel,
	"fieldLabel":      fieldLabel,
	"percent":         percent,
	"progressPercent": progressPercent,
}

// progressPercent avoids doing integer arithmetic inside the template
// (html/template has no built-in +-*/) for the batch progress bar width.
func progressPercent(completed, total int) int {
	if total == 0 {
		return 0
	}
	return completed * 100 / total
}

// verdictClass maps a verdict to a CSS class name — kept as one small,
// obvious mapping so the color-coding an agent relies on (pass/review/fail)
// can't drift out of sync between templates.
func verdictClass(v model.Verdict) string {
	switch v {
	case model.VerdictPass:
		return "pass"
	case model.VerdictNeedsReview:
		return "review"
	default:
		return "fail"
	}
}

func verdictLabel(v model.Verdict) string {
	switch v {
	case model.VerdictPass:
		return "PASS"
	case model.VerdictNeedsReview:
		return "NEEDS REVIEW"
	default:
		return "FAIL"
	}
}

// fieldLabel turns "brand_name" into "Brand Name" for display.
func fieldLabel(field string) string {
	words := strings.Split(field, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func percent(f float64) string {
	return fmt.Sprintf("%.0f%%", f*100)
}
