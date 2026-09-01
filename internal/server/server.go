// Package server wires up the HTTP handlers: single-label verify, batch
// submit, and batch status polling (consumed by htmx on the frontend).
package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/andrewlawlor/ttb-label-verifier/internal/batch"
	"github.com/andrewlawlor/ttb-label-verifier/internal/match"
	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

// verifyTimeout is the end-to-end budget for a single synchronous verify
// request, per SPEC.md section 2 ("about 5 seconds, or nobody uses it").
const verifyTimeout = 9 * time.Second

// pdfExtractTimeout bounds the optional application-PDF brand-name lookup.
// Generous relative to verifyTimeout since it's a separate, best-effort
// pre-fill request, not on the critical path of the verify itself.
const pdfExtractTimeout = 15 * time.Second

type Extractor interface {
	Extract(ctx context.Context, imageBytes []byte, mediaType string) (model.ExtractedFields, error)
}

// BrandNameExtractor pulls Brand Name off an uploaded TTB Form 5100.31 PDF
// — see internal/extract/pdfform.go for why that's the only field a real
// submitted form actually provides. Optional: a nil BrandNameExtractor
// disables the PDF upload feature (e.g. pdftoppm isn't installed) without
// affecting the rest of the app.
type BrandNameExtractor interface {
	ExtractBrandName(ctx context.Context, pdfBytes []byte) (model.FieldExtraction, error)
}

type Server struct {
	extractor    Extractor
	pdfExtractor BrandNameExtractor
	batch        *batch.Manager
	templates    *template.Template
	mux          *http.ServeMux
	version      string
}

// New wires up the HTTP handlers. version is the git commit SHA the
// running binary was built from (injected at build time — see Dockerfile
// and DEPLOY.md), exposed at GET /version so "is the server running the
// latest code" is answerable without SSHing in to check by hand. "dev" if
// unset (e.g. `go run` during local development).
func New(extractor Extractor, pdfExtractor BrandNameExtractor, batchMgr *batch.Manager, templatesFS embed.FS, staticFS embed.FS, version string) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templatesFS, "web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	if version == "" {
		version = "dev"
	}

	s := &Server{
		extractor:    extractor,
		pdfExtractor: pdfExtractor,
		batch:        batchMgr,
		templates:    tmpl,
		mux:          http.NewServeMux(),
		version:      version,
	}

	staticSub, err := staticServeFS(staticFS)
	if err != nil {
		return nil, err
	}

	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /batch", s.handleBatchPage)
	s.mux.HandleFunc("POST /api/verify", s.handleVerify)
	s.mux.HandleFunc("POST /api/extract-brand-name", s.handleExtractBrandName)
	s.mux.HandleFunc("POST /api/verify/batch", s.handleVerifyBatch)
	s.mux.HandleFunc("GET /api/verify/batch/{id}", s.handleBatchStatus)
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /version", s.handleVersion)

	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(s.version + "\n"))
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, "index.html", indexView{
		BrandName:        brandNameFieldView{},
		CanonicalWarning: match.CanonicalGovernmentWarning,
	})
}

// indexView is the single-label page's template data.
type indexView struct {
	BrandName        brandNameFieldView
	CanonicalWarning string
}

// brandNameFieldView renders the "brand_name_field" partial: the Brand
// Name label+input, either empty (initial page load) or pre-filled from
// an uploaded PDF, with a short note explaining what happened.
type brandNameFieldView struct {
	Value string
	Note  string
}

// handleExtractBrandName reads Item 6 off an uploaded TTB Form 5100.31 PDF
// and returns the brand_name field pre-filled for the agent to review —
// never submitted directly, always a suggestion the human can edit before
// the actual verify request.
func (s *Server) handleExtractBrandName(w http.ResponseWriter, r *http.Request) {
	if s.pdfExtractor == nil {
		s.render(w, "brand_name_field", brandNameFieldView{
			Note: "PDF reading isn't available on this deployment — enter the brand name manually.",
		})
		return
	}

	if err := r.ParseMultipartForm(20 << 20); err != nil {
		s.render(w, "brand_name_field", brandNameFieldView{Note: "Could not read the uploaded PDF."})
		return
	}

	file, _, err := r.FormFile("application_pdf")
	if err != nil {
		s.render(w, "brand_name_field", brandNameFieldView{}) // no file selected — nothing to do
		return
	}
	defer file.Close()

	const maxPDFSize = 20 << 20 // 20MB
	data, err := io.ReadAll(io.LimitReader(file, maxPDFSize+1))
	if err != nil || len(data) > maxPDFSize {
		s.render(w, "brand_name_field", brandNameFieldView{Note: "PDF too large or unreadable (max 20MB)."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), pdfExtractTimeout)
	defer cancel()

	result, err := s.pdfExtractor.ExtractBrandName(ctx, data)
	if err != nil || result.Value == "" {
		s.render(w, "brand_name_field", brandNameFieldView{
			Note: "Couldn't find a brand name on that form — enter it manually.",
		})
		return
	}

	s.render(w, "brand_name_field", brandNameFieldView{
		Value: result.Value,
		Note:  "Extracted from the uploaded PDF — please review before submitting.",
	})
}

func (s *Server) handleBatchPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "batch.html", nil)
}

// handleVerify handles a single label: one image + application fields,
// synchronous response within the 5s-ish budget.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), verifyTimeout)
	defer cancel()

	if err := r.ParseMultipartForm(20 << 20); err != nil {
		s.renderError(w, "Could not read the uploaded form. Please try again.", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("label_image")
	if err != nil {
		s.renderError(w, "No label image was uploaded.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	imageBytes, mediaType, err := readImage(file, header)
	if err != nil {
		s.renderError(w, err.Error(), http.StatusBadRequest)
		return
	}

	app := model.ApplicationFields{
		BrandName:      r.FormValue("brand_name"),
		ClassType:      r.FormValue("class_type"),
		AlcoholContent: r.FormValue("alcohol_content"),
		NetContents:    r.FormValue("net_contents"),
	}

	result := verifyOne(ctx, s.extractor, header.Filename, app, imageBytes, mediaType)

	s.render(w, "result_fragment.html", result)
}

// handleVerifyBatch accepts N images + a CSV manifest mapping filename to
// application fields, and starts an async batch job.
//
// Manifest CSV columns: filename,brand_name,class_type,alcohol_content,net_contents
// (no government_warning column — see parseManifest)
func (s *Server) handleVerifyBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		s.renderError(w, "Could not read the uploaded batch. Please try again.", http.StatusBadRequest)
		return
	}

	manifestFile, _, err := r.FormFile("manifest")
	if err != nil {
		s.renderError(w, "No manifest CSV was uploaded.", http.StatusBadRequest)
		return
	}
	defer manifestFile.Close()

	manifest, err := parseManifest(manifestFile)
	if err != nil {
		s.renderError(w, "Manifest CSV: "+err.Error(), http.StatusBadRequest)
		return
	}

	imageFiles := r.MultipartForm.File["label_images"]
	items := make([]batch.Item, 0, len(imageFiles))
	for _, header := range imageFiles {
		f, err := header.Open()
		if err != nil {
			continue
		}
		imageBytes, mediaType, err := readImage(f, header)
		f.Close()
		if err != nil {
			continue
		}
		app, ok := manifest[header.Filename]
		var skipReason string
		if !ok {
			// No manifest row for this file. Report it as its own clear
			// error rather than silently running it through the matcher
			// against blank application data — that would produce a full
			// mismatch indistinguishable from a label that genuinely
			// fails every field, which is actively misleading for a
			// typo'd filename rather than just unhelpful.
			skipReason = fmt.Sprintf(
				"no manifest row found for filename %q — check that it matches exactly (case-sensitive)",
				header.Filename)
		}
		items = append(items, batch.Item{
			Filename:    header.Filename,
			ImageBytes:  imageBytes,
			MediaType:   mediaType,
			Application: app,
			SkipReason:  skipReason,
		})
	}

	if len(items) == 0 {
		s.renderError(w, "No readable label images were found in the upload.", http.StatusBadRequest)
		return
	}

	// Deliberately not tied to the request context — the batch must keep
	// running after this handler returns the batch ID.
	id := s.batch.Submit(context.Background(), items)

	s.render(w, "batch_status_fragment.html", s.batch.Status(id))
}

func (s *Server) handleBatchStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	status := s.batch.Status(id)
	if status == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "batch_status_fragment.html", status)
}

func verifyOne(ctx context.Context, extractor Extractor, filename string, app model.ApplicationFields, imageBytes []byte, mediaType string) model.VerifyResult {
	extracted, err := extractor.Extract(ctx, imageBytes, mediaType)
	if err != nil {
		return model.VerifyResult{
			ID:             newID(),
			Filename:       filename,
			OverallVerdict: model.VerdictFail,
			Error:          err.Error(),
		}
	}
	return match.Result(newID(), filename, app, extracted)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func staticServeFS(staticFS embed.FS) (fs.FS, error) {
	return fs.Sub(staticFS, "web/static")
}

func readImage(file multipart.File, header *multipart.FileHeader) ([]byte, string, error) {
	const maxImageSize = 10 << 20 // 10MB
	data, err := io.ReadAll(io.LimitReader(file, maxImageSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("could not read image data")
	}
	if len(data) > maxImageSize {
		return nil, "", fmt.Errorf("image too large (max 10MB)")
	}
	mediaType := header.Header.Get("Content-Type")
	switch mediaType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return nil, "", fmt.Errorf("unsupported image type %q — use JPEG, PNG, or WebP", mediaType)
	}
	return data, mediaType, nil
}

func parseManifest(r io.Reader) (map[string]model.ApplicationFields, error) {
	cr := csv.NewReader(r)
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("could not parse CSV: %w", err)
	}
	if len(rows) < 1 {
		return nil, fmt.Errorf("empty file")
	}

	header := rows[0]
	col := map[string]int{}
	for i, name := range header {
		col[strings.TrimSpace(strings.ToLower(name))] = i
	}
	// No government_warning column: that field isn't applicant-declared
	// data (see model.ApplicationFields), so every row is checked against
	// match.CanonicalGovernmentWarning regardless of what's in the CSV.
	required := []string{"filename", "brand_name", "class_type", "alcohol_content", "net_contents"}
	for _, c := range required {
		if _, ok := col[c]; !ok {
			return nil, fmt.Errorf("missing required column %q", c)
		}
	}

	out := make(map[string]model.ApplicationFields, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) < len(header) {
			continue
		}
		out[row[col["filename"]]] = model.ApplicationFields{
			BrandName:      row[col["brand_name"]],
			ClassType:      row[col["class_type"]],
			AlcoholContent: row[col["alcohol_content"]],
			NetContents:    row[col["net_contents"]],
		}
	}
	return out, nil
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) renderError(w http.ResponseWriter, message string, status int) {
	w.WriteHeader(status)
	s.render(w, "error_fragment.html", message)
}
