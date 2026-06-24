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

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
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
