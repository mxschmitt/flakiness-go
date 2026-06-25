package upload

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/andybalholm/brotli"

	"github.com/mxschmitt/flakiness-go/report"
)

func init() {
	// Make retries instant in tests (preserve the number of attempts).
	for i := range httpBackoff {
		httpBackoff[i] = 0
	}
}

func TestUpload_HappyPath(t *testing.T) {
	var gotReport report.Report
	var startAuth, finishAuth string
	var reportPut, finished bool

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var startBody map[string]string
	mux.HandleFunc("/api/upload/start", func(w http.ResponseWriter, r *http.Request) {
		startAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&startBody)
		json.NewEncoder(w).Encode(startResponse{
			UploadToken:        "utok",
			PresignedReportURL: srv.URL + "/put-report",
			WebURL:             "/org/proj/run/1",
		})
	})
	mux.HandleFunc("/put-report", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("report method = %s, want PUT", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("report content-type = %q", ct)
		}
		// The report must be brotli-compressed with Content-Encoding: br.
		if enc := r.Header.Get("Content-Encoding"); enc != "br" {
			t.Errorf("report content-encoding = %q, want br", enc)
		}
		body, _ := io.ReadAll(brotli.NewReader(r.Body))
		json.Unmarshal(body, &gotReport)
		reportPut = true
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/upload/finish", func(w http.ResponseWriter, r *http.Request) {
		finishAuth = r.Header.Get("Authorization")
		finished = true
		w.WriteHeader(http.StatusOK)
	})

	rep := &report.Report{Category: "go", CommitID: "abc", FlakinessProject: "max/flakiness-go"}
	client := New(srv.URL)
	url, err := client.Upload(rep, nil, "secret-token")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	// /start must carry orgSlug/projectSlug split from flakinessProject (SDK parity).
	if startBody["orgSlug"] != "max" || startBody["projectSlug"] != "flakiness-go" {
		t.Errorf("start body = %+v, want orgSlug=max projectSlug=flakiness-go", startBody)
	}
	if url != srv.URL+"/org/proj/run/1" {
		t.Errorf("returned url = %q", url)
	}
	if startAuth != "Bearer secret-token" {
		t.Errorf("start auth = %q, want Bearer secret-token", startAuth)
	}
	if finishAuth != "Bearer utok" {
		t.Errorf("finish auth = %q, want Bearer utok (upload token)", finishAuth)
	}
	if !reportPut || !finished {
		t.Errorf("reportPut=%v finished=%v, both should be true", reportPut, finished)
	}
	if gotReport.CommitID != "abc" {
		t.Errorf("uploaded report commit = %q, want abc", gotReport.CommitID)
	}
}

func TestUpload_RetriesOn503(t *testing.T) {
	var startCalls int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/api/upload/start", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&startCalls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(startResponse{
			UploadToken:        "utok",
			PresignedReportURL: srv.URL + "/put-report",
			WebURL:             "/run/1",
		})
	})
	mux.HandleFunc("/put-report", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/api/upload/finish", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	client := New(srv.URL)
	if _, err := client.Upload(&report.Report{}, nil, "tok"); err != nil {
		t.Fatalf("Upload should succeed after retries: %v", err)
	}
	if got := atomic.LoadInt32(&startCalls); got != 3 {
		t.Errorf("start called %d times, want 3 (2 failures + 1 success)", got)
	}
}

func TestUpload_StartFailsHard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	client := New(srv.URL)
	if _, err := client.Upload(&report.Report{}, nil, "tok"); err == nil {
		t.Fatal("expected error on persistent 401 start")
	}
}

func TestUpload_RetriesOn4xx(t *testing.T) {
	// The Node SDK's fetchOk throws on ANY non-2xx and retryWithBackoff retries
	// all errors, so a transient 4xx is retried (not just 5xx).
	var calls int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/api/upload/start", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			w.WriteHeader(http.StatusTooManyRequests) // 429, a 4xx
			return
		}
		json.NewEncoder(w).Encode(startResponse{UploadToken: "utok", PresignedReportURL: srv.URL + "/put", WebURL: "/run/1"})
	})
	mux.HandleFunc("/put", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/api/upload/finish", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	client := New(srv.URL)
	if _, err := client.Upload(&report.Report{}, nil, "tok"); err != nil {
		t.Fatalf("Upload should retry the 429 and succeed: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("start called %d times, want 2 (1 retried 429 + success)", got)
	}
}

func TestIsCompressible(t *testing.T) {
	compressible := []string{"text/plain", "text/html; charset=utf-8", "image/svg+xml", "application/manifest+json", "application/foo+text"}
	for _, ct := range compressible {
		if !isCompressible(ct) {
			t.Errorf("isCompressible(%q) = false, want true", ct)
		}
	}
	// The SDK does NOT compress bare application/json or application/xml.
	notCompressible := []string{"application/json", "application/xml", "image/png", "application/octet-stream", ""}
	for _, ct := range notCompressible {
		if isCompressible(ct) {
			t.Errorf("isCompressible(%q) = true, want false (matches SDK)", ct)
		}
	}
}

func TestUpload_Attachments(t *testing.T) {
	// Write a text (compressible) and a binary (raw) attachment to disk.
	dir := t.TempDir()
	textPath := filepath.Join(dir, "log.txt")
	binPath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(textPath, []byte("hello log content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}

	type put struct {
		encoding    string
		contentType string
		body        []byte
	}
	puts := map[string]put{}
	var attIDs []string

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/api/upload/start", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(startResponse{UploadToken: "utok", PresignedReportURL: srv.URL + "/put/report", WebURL: "/run/1"})
	})
	mux.HandleFunc("/api/upload/attachments", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AttachmentIds []string `json:"attachmentIds"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		attIDs = body.AttachmentIds
		var resp []attachmentURL
		for _, id := range body.AttachmentIds {
			resp = append(resp, attachmentURL{AttachmentID: id, PresignedURL: srv.URL + "/put/" + id})
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/put/", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		puts[r.URL.Path] = put{encoding: r.Header.Get("Content-Encoding"), contentType: r.Header.Get("Content-Type"), body: raw}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/upload/finish", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	atts := []Attachment{
		{ID: "txt1", ContentType: "text/plain", Path: textPath},
		{ID: "bin1", ContentType: "image/png", Path: binPath},
	}
	client := New(srv.URL)
	if _, err := client.Upload(&report.Report{Category: "go", CommitID: "abc"}, atts, "tok"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if len(attIDs) != 2 {
		t.Errorf("attachment ids sent = %v, want 2", attIDs)
	}
	// Text attachment: brotli-compressed with Content-Encoding: br, decodes back.
	txt := puts["/put/txt1"]
	if txt.encoding != "br" {
		t.Errorf("text attachment encoding = %q, want br", txt.encoding)
	}
	if txt.contentType != "text/plain" {
		t.Errorf("text attachment content-type = %q", txt.contentType)
	}
	dec, _ := io.ReadAll(brotli.NewReader(bytes.NewReader(txt.body)))
	if string(dec) != "hello log content" {
		t.Errorf("decompressed text = %q, want original", dec)
	}
	// Binary attachment: raw, no Content-Encoding.
	bin := puts["/put/bin1"]
	if bin.encoding != "" {
		t.Errorf("binary attachment must not be compressed, got encoding %q", bin.encoding)
	}
	if len(bin.body) != 7 {
		t.Errorf("binary attachment body len = %d, want 7 (raw)", len(bin.body))
	}
}
