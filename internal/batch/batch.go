// Package batch runs a bounded-concurrency worker pool over a set of
// labels so a 200-300 label batch (Sarah's "big importers dump 300 on us
// at once" case) finishes in a couple of minutes rather than serially at
// ~5s/label (~25 minutes) — see SPEC.md section 7.
package batch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/andrewlawlor/ttb-label-verifier/internal/match"
	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

// maxConcurrency bounds how many extraction calls are in flight at once.
// High enough to make 200-300 labels finish quickly, low enough not to
// slam the vision API or blow through rate limits.
const maxConcurrency = 15

// Extractor is the subset of *extract.Client the batch worker pool needs.
// Defined here (consumer side) so tests can supply a fake instead of
// making live API calls for every worker-pool/concurrency test.
type Extractor interface {
	Extract(ctx context.Context, imageBytes []byte, mediaType string) (model.ExtractedFields, error)
}

type Item struct {
	Filename    string
	ImageBytes  []byte
	MediaType   string
	Application model.ApplicationFields
	// SkipReason, if set, short-circuits processing: no extraction call is
	// made, and the result is reported as this error instead. Used for a
	// filename with no matching manifest row, so that shows up as an
	// unambiguous "couldn't check this" rather than silently running the
	// image through the matcher against blank application data, which
	// would produce a full mismatch indistinguishable from a label that
	// genuinely fails every field.
	SkipReason string
}

// job wraps a BatchStatus with the single mutex that guards every read and
// write of it — Submit's background goroutines and Status's polling reads
// must share one lock, not two independent ones, or progress reads race
// with progress writes.
type job struct {
	mu     sync.Mutex
	status model.BatchStatus
}

// Manager holds in-memory batch job state for the lifetime of the process.
// Per SPEC.md section 2 ("prototype only, no real PII storage"), this is
// intentionally not persisted to a database.
type Manager struct {
	client Extractor

	mu   sync.RWMutex
	jobs map[string]*job
}

func NewManager(client Extractor) *Manager {
	return &Manager{
		client: client,
		jobs:   make(map[string]*job),
	}
}

// Submit starts processing items in the background and returns a job ID
// immediately; call Status to poll progress.
func (m *Manager) Submit(ctx context.Context, items []Item) string {
	id := newID()
	j := &job{
		status: model.BatchStatus{
			ID:      id,
			Total:   len(items),
			Results: make([]model.VerifyResult, len(items)),
		},
	}

	m.mu.Lock()
	m.jobs[id] = j
	m.mu.Unlock()

	go run(ctx, m.client, j, items)

	return id
}

func run(ctx context.Context, client Extractor, j *job, items []Item) {
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, item Item) {
			defer wg.Done()
			defer func() { <-sem }()

			result := processOne(ctx, client, item)

			j.mu.Lock()
			j.status.Results[i] = result
			j.status.Completed++
			j.mu.Unlock()
		}(i, item)
	}

	wg.Wait()

	j.mu.Lock()
	j.status.Done = true
	j.mu.Unlock()
}

func processOne(ctx context.Context, client Extractor, item Item) model.VerifyResult {
	id := newID()
	if item.SkipReason != "" {
		return model.VerifyResult{
			ID:             id,
			Filename:       item.Filename,
			OverallVerdict: model.VerdictFail,
			Error:          item.SkipReason,
		}
	}
	extracted, err := client.Extract(ctx, item.ImageBytes, item.MediaType)
	if err != nil {
		return model.VerifyResult{
			ID:             id,
			Filename:       item.Filename,
			OverallVerdict: model.VerdictFail,
			Error:          err.Error(),
		}
	}
	return match.Result(id, item.Filename, item.Application, extracted)
}

// Status returns a snapshot of the job's current progress, or nil if the
// ID is unknown.
func (m *Manager) Status(id string) *model.BatchStatus {
	m.mu.RLock()
	j, ok := m.jobs[id]
	m.mu.RUnlock()
	if !ok {
		return nil
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	snapshot := j.status
	snapshot.Results = append([]model.VerifyResult(nil), j.status.Results...)
	return &snapshot
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
