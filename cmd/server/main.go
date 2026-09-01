// Command server runs the TTB label verification prototype: a small HTTP
// service that checks a label photo against submitted application data,
// singly or in batch. See SPEC.md for the design.
package main

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/andrewlawlor/ttb-label-verifier/internal/batch"
	"github.com/andrewlawlor/ttb-label-verifier/internal/extract"
	"github.com/andrewlawlor/ttb-label-verifier/internal/server"
	"github.com/andrewlawlor/ttb-label-verifier/internal/webassets"
)

// Default extraction backend is local OCR, not the Claude vision API: this
// app is meant to be deployed at a public URL for evaluation, and a paid
// API sitting behind a public endpoint is a real cost/abuse vector, not
// just latency risk. OCR is free, has no outbound dependency (see Marcus's
// firewall story in SPEC.md), and deterministically fits the ~5s budget.
// EXTRACTION_BACKEND=claude switches to the vision API — kept working and
// documented as the more robust option for a real production discussion.
const (
	backendOCR    = "ocr"
	backendClaude = "claude"
)

// version is the git commit SHA this binary was built from, injected via
// -ldflags "-X main.version=..." at build time (see Dockerfile). Stays
// "dev" for a plain `go run`/`go build`, which is expected and fine.
var version = "dev"

func main() {
	backend := os.Getenv("EXTRACTION_BACKEND")
	if backend == "" {
		backend = backendOCR
	}

	var extractor server.Extractor
	switch backend {
	case backendClaude:
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			log.Println("warning: ANTHROPIC_API_KEY is not set — label extraction calls will fail until it is")
		}
		extractor = extract.New(apiKey)
		log.Println("extraction backend: Claude vision API")
	case backendOCR:
		if _, err := exec.LookPath("tesseract"); err != nil {
			log.Fatal("EXTRACTION_BACKEND=ocr requires the `tesseract` binary on PATH " +
				"(apt install tesseract-ocr); set EXTRACTION_BACKEND=claude to use the Claude vision API instead")
		}
		extractor = extract.NewOCR()
		log.Println("extraction backend: local OCR (tesseract)")
	default:
		log.Fatalf("unknown EXTRACTION_BACKEND %q (want %q or %q)", backend, backendOCR, backendClaude)
	}

	var pdfExtractor server.BrandNameExtractor
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		log.Println("warning: pdftoppm not found — application-PDF brand-name pre-fill will be disabled " +
			"(install poppler-utils to enable it); label verification is unaffected")
	} else {
		pdfExtractor = extract.NewPDFForm()
	}

	batchMgr := batch.NewManager(extractor)

	srv, err := server.New(extractor, pdfExtractor, batchMgr, webassets.TemplatesFS, webassets.StaticFS, version)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
	log.Printf("version: %s", version)

	addr := ":" + port()
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second, // generous enough to cover batch-submit uploads, not just single-label verify
	}

	log.Printf("listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}
