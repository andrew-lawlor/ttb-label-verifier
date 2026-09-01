// Command server runs the TTB label verification prototype: a small HTTP
// service that checks a label photo against submitted application data,
// singly or in batch. See SPEC.md for the design.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/andrewlawlor/ttb-label-verifier/internal/batch"
	"github.com/andrewlawlor/ttb-label-verifier/internal/extract"
	"github.com/andrewlawlor/ttb-label-verifier/internal/server"
	"github.com/andrewlawlor/ttb-label-verifier/internal/webassets"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Println("warning: ANTHROPIC_API_KEY is not set — label extraction calls will fail until it is")
	}

	extractor := extract.New(apiKey)
	batchMgr := batch.NewManager(extractor)

	srv, err := server.New(extractor, batchMgr, webassets.TemplatesFS, webassets.StaticFS)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}

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
