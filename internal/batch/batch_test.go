package batch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/andrewlawlor/ttb-label-verifier/internal/match"
	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

// fakeExtractor lets us exercise the worker pool and progress tracking
// without hitting the real vision API.
type fakeExtractor struct {
	delay   time.Duration
	failFor map[string]bool // filenames that should return an error
}

func (f fakeExtractor) Extract(ctx context.Context, imageBytes []byte, mediaType string) (model.ExtractedFields, error) {
	time.Sleep(f.delay)
	name := string(imageBytes) // tests pass the filename as the "image" for identification
	if f.failFor[name] {
		return model.ExtractedFields{}, fmt.Errorf("simulated extraction failure for %s", name)
	}
	v := model.FieldExtraction{Value: "match", Confidence: 0.95}
	return model.ExtractedFields{
		BrandName:         v,
		ClassType:         v,
		AlcoholContent:    model.FieldExtraction{Value: "45%", Confidence: 0.95},
		NetContents:       model.FieldExtraction{Value: "750 mL", Confidence: 0.95},
		GovernmentWarning: model.FieldExtraction{Value: match.CanonicalGovernmentWarning, Confidence: 0.95},
	}, nil
}

func TestSubmitProcessesAllItemsAndReachesDone(t *testing.T) {
	items := make([]Item, 50)
	for i := range items {
		name := fmt.Sprintf("label-%d", i)
		items[i] = Item{Filename: name, ImageBytes: []byte(name)}
	}

	m := NewManager(fakeExtractor{delay: 5 * time.Millisecond})
	id := m.Submit(context.Background(), items)

	deadline := time.Now().Add(5 * time.Second)
	var status *model.BatchStatus
	for time.Now().Before(deadline) {
		status = m.Status(id)
		if status.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if status == nil || !status.Done {
		t.Fatalf("batch did not complete in time: %+v", status)
	}
	if status.Completed != len(items) {
		t.Fatalf("expected %d completed, got %d", len(items), status.Completed)
	}
	for i, r := range status.Results {
		if r.ID == "" {
			t.Fatalf("result %d was never written", i)
		}
	}
}

func TestSubmitReportsPartialFailuresWithoutStoppingBatch(t *testing.T) {
	matchingApp := model.ApplicationFields{
		BrandName: "match", ClassType: "match", AlcoholContent: "45%", NetContents: "750 mL",
	}
	items := []Item{
		{Filename: "ok-1", ImageBytes: []byte("ok-1"), Application: matchingApp},
		{Filename: "bad", ImageBytes: []byte("bad"), Application: matchingApp},
		{Filename: "ok-2", ImageBytes: []byte("ok-2"), Application: matchingApp},
	}

	m := NewManager(fakeExtractor{failFor: map[string]bool{"bad": true}})
	id := m.Submit(context.Background(), items)

	deadline := time.Now().Add(5 * time.Second)
	var status *model.BatchStatus
	for time.Now().Before(deadline) {
		status = m.Status(id)
		if status.Done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if status == nil || !status.Done {
		t.Fatalf("batch did not complete in time")
	}
	if status.Completed != 3 {
		t.Fatalf("expected all 3 items to be accounted for, got %d", status.Completed)
	}

	var sawFailure bool
	for _, r := range status.Results {
		if r.Filename == "bad" {
			sawFailure = true
			if r.OverallVerdict != model.VerdictFail || r.Error == "" {
				t.Fatalf("expected failed item to carry an error, got %+v", r)
			}
		}
		if r.Filename == "ok-1" || r.Filename == "ok-2" {
			if r.OverallVerdict != model.VerdictPass {
				t.Fatalf("expected %s to pass, got %+v", r.Filename, r)
			}
		}
	}
	if !sawFailure {
		t.Fatalf("expected to find the failed item in results")
	}
}

func TestStatusUnknownIDReturnsNil(t *testing.T) {
	m := NewManager(fakeExtractor{})
	if got := m.Status("does-not-exist"); got != nil {
		t.Fatalf("expected nil for unknown job id, got %+v", got)
	}
}
