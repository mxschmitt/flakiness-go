// Package oidc fetches GitHub Actions OIDC tokens for keyless upload auth.
package oidc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// GithubOIDC requests an OIDC token from the GitHub Actions token service.
type GithubOIDC struct {
	requestURL   string
	requestToken string
	client       *http.Client
}

// FromEnv returns a GithubOIDC configured from the GitHub Actions environment,
// or nil when the required variables are absent (i.e. not running in Actions).
func FromEnv() *GithubOIDC {
	reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if reqURL == "" || reqToken == "" {
		return nil
	}
	return &GithubOIDC{
		requestURL:   reqURL,
		requestToken: reqToken,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

// FetchToken requests a token whose audience claim is set to the given value
// (the flakiness project identifier).
func (g *GithubOIDC) FetchToken(audience string) (string, error) {
	u, err := url.Parse(g.requestURL)
	if err != nil {
		return "", fmt.Errorf("invalid OIDC request URL: %w", err)
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()

	// Retry on transient failures with the same backoff schedule the Node SDK
	// uses for its GET requests (getJSON over HTTP_BACKOFF).
	var lastErr error
	for attempt := 0; attempt <= len(oidcBackoff); attempt++ {
		token, err := g.fetchOnce(u.String())
		if err == nil {
			return token, nil
		}
		lastErr = err
		if attempt < len(oidcBackoff) {
			time.Sleep(oidcBackoff[attempt])
		}
	}
	return "", lastErr
}

// oidcBackoff mirrors the Node SDK's HTTP_BACKOFF (_internalUtils.ts).
// Overridable in tests.
var oidcBackoff = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	1000 * time.Millisecond,
	1000 * time.Millisecond,
	1000 * time.Millisecond,
	1000 * time.Millisecond,
}

func (g *GithubOIDC) fetchOnce(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "bearer "+g.requestToken)
	req.Header.Set("Accept", "application/json; api-version=2.0")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("failed to request GitHub OIDC token: %d %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.Value == "" {
		return "", fmt.Errorf("GitHub OIDC token response did not contain a token value")
	}
	return parsed.Value, nil
}
