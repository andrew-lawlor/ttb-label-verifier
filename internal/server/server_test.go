package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewlawlor/ttb-label-verifier/internal/batch"
	"github.com/andrewlawlor/ttb-label-verifier/internal/match"
	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
	"github.com/andrewlawlor/ttb-label-verifier/internal/webassets"
)

// fakeExtractor lets tests control extraction results without a real OCR
// binary or network call, and counts invocations so tests can assert a
// skipped batch item never reaches it (see TestHandleVerifyBatch_MissingManifestRowIsSkipped).
type fakeExtractor struct {
	result model.ExtractedFields
	err    error
	calls  atomic.Int32
}

func (f *fakeExtractor) Extract(ctx context.Context, imageBytes []byte, mediaType string) (model.ExtractedFields, error) {
	f.calls.Add(1)
	return f.result, f.err
}

// matchingExtraction returns extracted fields that pass every check against app.
func matchingExtraction(app model.ApplicationFields) model.ExtractedFields {
	return model.ExtractedFields{
		BrandName:         model.FieldExtraction{Value: app.BrandName, Confidence: 0.99},
		ClassType:         model.FieldExtraction{Value: app.ClassType, Confidence: 0.99},
		AlcoholContent:    model.FieldExtraction{Value: app.AlcoholContent, Confidence: 0.99},
		NetContents:       model.FieldExtraction{Value: app.NetContents, Confidence: 0.99},
		GovernmentWarning: model.FieldExtraction{Value: match.CanonicalGovernmentWarning, Confidence: 0.99},
	}
}

func newTestServer(t *testing.T, extractor Extractor, pdfExtractor BrandNameExtractor) *Server {
	t.Helper()
	// batch.Extractor and server.Extractor declare the same single method,
	// so a server.Extractor value is directly assignable here.
	batchMgr := batch.NewManager(extractor)
	s, err := New(extractor, pdfExtractor, batchMgr, webassets.TemplatesFS, webassets.StaticFS, "test-sha")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

const testJPEGContentType = "image/jpeg"

// jpegPart writes a form file part with an explicit Content-Type, since
// multipart.Writer.CreateFormFile always defaults to
// application/octet-stream and readImage() rejects that.
func jpegPart(w *multipart.Writer, field, filename string, data []byte) error {
	part, err := w.CreatePart(mimeHeader(field, filename, testJPEGContentType))
	if err != nil {
		return err
	}
	_, err = part.Write(data)
	return err
}

func mimeHeader(field, filename, contentType string) map[string][]string {
	return map[string][]string{
		"Content-Disposition": {`form-data; name="` + field + `"; filename="` + filename + `"`},
		"Content-Type":        {contentType},
	}
}

func TestHandleHealth(t *testing.T) {
	s := newTestServer(t, &fakeExtractor{}, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleVersion(t *testing.T) {
	s := newTestServer(t, &fakeExtractor{}, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "test-sha" {
		t.Fatalf("body = %q, want %q", got, "test-sha")
	}
}

func TestHandleIndex(t *testing.T) {
	s := newTestServer(t, &fakeExtractor{}, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), match.CanonicalGovernmentWarning) {
		t.Errorf("index page does not render the canonical government warning text read-only")
	}
}

func TestHandleVerify_Success(t *testing.T) {
	app := model.ApplicationFields{
		BrandName:      "Stone's Throw",
		ClassType:      "Kentucky Straight Bourbon Whiskey",
		AlcoholContent: "45% Alc./Vol.",
		NetContents:    "750 mL",
	}
	extractor := &fakeExtractor{result: matchingExtraction(app)}
	s := newTestServer(t, extractor, nil)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := jpegPart(w, "label_image", "label.jpg", []byte("fake-jpeg-bytes")); err != nil {
		t.Fatal(err)
	}
	_ = w.WriteField("brand_name", app.BrandName)
	_ = w.WriteField("class_type", app.ClassType)
	_ = w.WriteField("alcohol_content", app.AlcoholContent)
	_ = w.WriteField("net_contents", app.NetContents)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/verify", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if extractor.calls.Load() != 1 {
		t.Errorf("extractor called %d times, want 1", extractor.calls.Load())
	}
	if !strings.Contains(rec.Body.String(), "PASS") {
		t.Errorf("expected a PASS verdict in response body, got: %s", rec.Body.String())
	}
}

func TestHandleVerify_NoImage(t *testing.T) {
	s := newTestServer(t, &fakeExtractor{}, nil)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("brand_name", "whatever")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/verify", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// Regression check for the header-ordering bug: renderError used to
	// call WriteHeader before render() set Content-Type, so it silently
	// never went out on the wire.
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html on an error response", ct)
	}
}

func TestHandleVerify_UnsupportedImageType(t *testing.T) {
	s := newTestServer(t, &fakeExtractor{}, nil)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreatePart(mimeHeader("label_image", "label.gif", "image/gif"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake-gif-bytes"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/verify", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "JPEG, PNG, or WebP") {
		t.Errorf("expected a clear supported-format message, got: %s", rec.Body.String())
	}
}

func TestHandleVerifyBatch_MissingManifestRowIsSkippedNotExtracted(t *testing.T) {
	extractor := &fakeExtractor{result: matchingExtraction(model.ApplicationFields{})}
	s := newTestServer(t, extractor, nil)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	manifest := "filename,brand_name,class_type,alcohol_content,net_contents\n" +
		"other-file.jpg,Old Tom,Bourbon,45%,750 mL\n"
	mpart, err := w.CreatePart(mimeHeader("manifest", "manifest.csv", "text/csv"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = mpart.Write([]byte(manifest))
	if err := jpegPart(w, "label_images", "label-not-in-manifest.jpg", []byte("fake-jpeg-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/verify/batch", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	status := waitForBatchDone(t, s, rec.Body.String())
	if len(status.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(status.Results))
	}
	got := status.Results[0]
	if got.Error == "" || !strings.Contains(got.Error, "no manifest row found") {
		t.Errorf("Error = %q, want a no-manifest-row message", got.Error)
	}
	if extractor.calls.Load() != 0 {
		t.Errorf("extractor called %d times for a skipped item, want 0", extractor.calls.Load())
	}
}

func TestHandleVerifyBatch_NoManifest(t *testing.T) {
	s := newTestServer(t, &fakeExtractor{}, nil)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := jpegPart(w, "label_images", "label.jpg", []byte("fake-jpeg-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/verify/batch", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleBatchStatus_UnknownID(t *testing.T) {
	s := newTestServer(t, &fakeExtractor{}, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/verify/batch/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// waitForBatchDone extracts the batch ID from a batch-status fragment
// response body's data-batch-id attribute (present unconditionally,
// unlike the hx-get polling URL which is omitted once Done) and polls the
// server directly until the job reports Done.
func waitForBatchDone(t *testing.T, s *Server, firstResponseBody string) *model.BatchStatus {
	t.Helper()
	const marker = `data-batch-id="`
	idx := strings.Index(firstResponseBody, marker)
	if idx == -1 {
		t.Fatalf("could not find data-batch-id in response: %s", firstResponseBody)
	}
	rest := firstResponseBody[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		t.Fatalf("could not parse batch id from response: %s", firstResponseBody)
	}
	id := rest[:end]

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := s.batch.Status(id)
		if status == nil {
			t.Fatalf("batch %q not found", id)
		}
		if status.Done {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("batch %q did not finish within deadline", id)
	return nil
}

func TestParseManifest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		csv := "filename,brand_name,class_type,alcohol_content,net_contents\n" +
			"a.jpg,Old Tom,Bourbon,45%,750 mL\n"
		got, err := parseManifest(strings.NewReader(csv))
		if err != nil {
			t.Fatalf("parseManifest: %v", err)
		}
		if got["a.jpg"].BrandName != "Old Tom" {
			t.Errorf("BrandName = %q, want %q", got["a.jpg"].BrandName, "Old Tom")
		}
	})

	t.Run("missing required column", func(t *testing.T) {
		csv := "filename,brand_name\na.jpg,Old Tom\n"
		if _, err := parseManifest(strings.NewReader(csv)); err == nil {
			t.Error("expected an error for a missing required column, got nil")
		}
	})

	t.Run("header names are trimmed and case-insensitive", func(t *testing.T) {
		csv := " Filename , Brand_Name ,Class_Type,Alcohol_Content,Net_Contents\n" +
			"a.jpg,Old Tom,Bourbon,45%,750 mL\n"
		got, err := parseManifest(strings.NewReader(csv))
		if err != nil {
			t.Fatalf("parseManifest: %v", err)
		}
		if _, ok := got["a.jpg"]; !ok {
			t.Errorf("expected a row for a.jpg, got %v", got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		if _, err := parseManifest(strings.NewReader("")); err == nil {
			t.Error("expected an error for an empty file, got nil")
		}
	})
}

func TestReadImage_TooLarge(t *testing.T) {
	// One byte past the 10MB cap in readImage().
	oversized := bytes.Repeat([]byte("a"), (10<<20)+1)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := jpegPart(w, "label_image", "label.jpg", oversized); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, &fakeExtractor{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/verify", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too large") {
		t.Errorf("expected a too-large message, got: %s", rec.Body.String())
	}
}
