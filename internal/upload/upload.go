// Package upload implements the Flakiness.io report upload protocol:
// start -> PUT report -> (attachments) -> finish. It mirrors the official
// Node SDK and pytest uploaders.
//
// The report body is brotli-compressed and sent with Content-Encoding: br —
// this is mandatory: the server mints a presigned URL for `report.json.br`
// that signs the `content-encoding` header, so an uncompressed PUT fails with
// SignatureDoesNotMatch. Compressible (text-like) attachments are likewise
// brotli-compressed; binary attachments are uploaded as-is.
package upload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/andybalholm/brotli"

	"github.com/mxschmitt/flakiness-go/report"
)

// compressBrotli brotli-compresses data at quality 6 (matching the Node SDK).
func compressBrotli(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, 6)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// isCompressible reports whether a content type should be brotli-compressed
// before upload. This mirrors the Node SDK's heuristic exactly
// (uploadReport.ts _uploadAttachment): text/* plus the +json/+text/+xml
// structured-suffix families. Note the SDK deliberately does NOT treat bare
// application/json or application/xml as compressible, so neither do we.
func isCompressible(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(ct, "text/") ||
		strings.HasSuffix(ct, "+json") ||
		strings.HasSuffix(ct, "+text") ||
		strings.HasSuffix(ct, "+xml")
}

// Attachment is a local file to upload alongside the report.
type Attachment struct {
	ID          string
	ContentType string
	Path        string
}

type startResponse struct {
	UploadToken        string `json:"uploadToken"`
	PresignedReportURL string `json:"presignedReportUrl"`
	WebURL             string `json:"webUrl"`
}

type attachmentURL struct {
	AttachmentID string `json:"attachmentId"`
	PresignedURL string `json:"presignedUrl"`
}

// Client performs uploads against a Flakiness.io endpoint.
type Client struct {
	Endpoint   string
	HTTPClient *http.Client
}

// New returns a Client with sensible timeouts.
func New(endpoint string) *Client {
	return &Client{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Upload runs the full protocol and returns the web URL of the uploaded run.
func (c *Client) Upload(rep *report.Report, attachments []Attachment, token string) (string, error) {
	// Step 1: start.
	start, err := c.start(token)
	if err != nil {
		return "", fmt.Errorf("upload start failed: %w", err)
	}

	// Step 2: PUT the report JSON, brotli-compressed (mandatory — see package doc).
	body, err := json.Marshal(rep)
	if err != nil {
		return "", err
	}
	compressed, err := compressBrotli(body)
	if err != nil {
		return "", fmt.Errorf("compressing report: %w", err)
	}
	if err := c.putBytes(start.PresignedReportURL, compressed, "application/json", "br"); err != nil {
		return "", fmt.Errorf("report upload failed: %w", err)
	}

	// Step 3: attachments (best-effort, parity with the reference).
	if err := c.uploadAttachments(start.UploadToken, attachments); err != nil {
		return "", fmt.Errorf("attachment upload failed: %w", err)
	}

	// Step 4: finish.
	if err := c.finish(start.UploadToken); err != nil {
		return "", fmt.Errorf("upload finish failed: %w", err)
	}
	return c.Endpoint + start.WebURL, nil
}

func (c *Client) start(token string) (*startResponse, error) {
	req, _ := http.NewRequest(http.MethodPost, c.Endpoint+"/api/upload/start", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.doWithRetry(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := expectOK(resp); err != nil {
		return nil, err
	}
	var s startResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *Client) finish(uploadToken string) error {
	req, _ := http.NewRequest(http.MethodPost, c.Endpoint+"/api/upload/finish", nil)
	req.Header.Set("Authorization", "Bearer "+uploadToken)
	resp, err := c.doWithRetry(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectOK(resp)
}

func (c *Client) uploadAttachments(uploadToken string, attachments []Attachment) error {
	if len(attachments) == 0 {
		return nil
	}
	ids := make([]string, len(attachments))
	for i, a := range attachments {
		ids[i] = a.ID
	}
	reqBody, _ := json.Marshal(map[string]any{"attachmentIds": ids})
	req, _ := http.NewRequest(http.MethodPost, c.Endpoint+"/api/upload/attachments", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+uploadToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.doWithRetry(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectOK(resp); err != nil {
		return err
	}
	var urls []attachmentURL
	if err := json.NewDecoder(resp.Body).Decode(&urls); err != nil {
		return err
	}
	byID := map[string]string{}
	for _, u := range urls {
		byID[u.AttachmentID] = u.PresignedURL
	}
	for _, a := range attachments {
		dst, ok := byID[a.ID]
		if !ok {
			continue
		}
		data, err := os.ReadFile(a.Path)
		if err != nil {
			continue
		}
		encoding := ""
		if isCompressible(a.ContentType) {
			if cdata, cerr := compressBrotli(data); cerr == nil {
				data = cdata
				encoding = "br"
			}
		}
		if err := c.putBytes(dst, data, a.ContentType, encoding); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) putBytes(url string, data []byte, contentType, contentEncoding string) error {
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", contentType)
	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}
	resp, err := c.doWithRetry(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectOK(resp)
}

// httpBackoff is the inter-attempt delay schedule, matching the Node SDK's
// HTTP_BACKOFF (_internalUtils.ts): 7 total attempts, capped at 1s. Overridable
// in tests.
var httpBackoff = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	1000 * time.Millisecond,
	1000 * time.Millisecond,
	1000 * time.Millisecond,
	1000 * time.Millisecond,
}

// doWithRetry retries on transient failures (network errors and retryable 5xx
// statuses) using httpBackoff, matching the reference uploader's retry policy.
// Non-retryable statuses (e.g. 4xx) are returned immediately for the caller to
// surface.
func (c *Client) doWithRetry(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
		body = b
	}
	attempts := len(httpBackoff) + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			lastErr = fmt.Errorf("server returned %d", resp.StatusCode)
		} else {
			return resp, nil
		}
		if attempt < len(httpBackoff) {
			time.Sleep(httpBackoff[attempt])
		}
	}
	return nil, lastErr
}

func expectOK(resp *http.Response) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
