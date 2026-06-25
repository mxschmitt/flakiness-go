package oidc

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func init() {
	// Make retries instant in tests (preserve attempt count).
	for i := range oidcBackoff {
		oidcBackoff[i] = 0
	}
}

func TestFromEnv_Absent(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	if g := FromEnv(); g != nil {
		t.Error("FromEnv should return nil when env vars are absent")
	}
}

func TestFromEnv_Present(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.example/req")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "reqtok")
	g := FromEnv()
	if g == nil {
		t.Fatal("FromEnv returned nil despite env vars present")
	}
}

func TestFetchToken(t *testing.T) {
	var gotAudience, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAudience = r.URL.Query().Get("audience")
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Write([]byte(`{"value":"the-oidc-token"}`))
	}))
	defer srv.Close()

	g := &GithubOIDC{
		requestURL:   srv.URL + "/req?foo=bar",
		requestToken: "reqtok",
		client:       &http.Client{Timeout: 5 * time.Second},
	}
	tok, err := g.FetchToken("max/flakiness-go")
	if err != nil {
		t.Fatalf("FetchToken: %v", err)
	}
	if tok != "the-oidc-token" {
		t.Errorf("token = %q", tok)
	}
	if gotAudience != "max/flakiness-go" {
		t.Errorf("audience = %q, want max/flakiness-go", gotAudience)
	}
	if gotAuth != "bearer reqtok" {
		t.Errorf("auth = %q, want bearer reqtok", gotAuth)
	}
	if gotAccept != "application/json; api-version=2.0" {
		t.Errorf("accept = %q", gotAccept)
	}
}

func TestFetchToken_EmptyValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"value":""}`))
	}))
	defer srv.Close()
	g := &GithubOIDC{requestURL: srv.URL, requestToken: "x", client: srv.Client()}
	if _, err := g.FetchToken("aud"); err == nil {
		t.Error("expected error when token value is empty")
	}
}

func TestFetchToken_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	g := &GithubOIDC{requestURL: srv.URL, requestToken: "x", client: srv.Client()}
	if _, err := g.FetchToken("aud"); err == nil {
		t.Error("expected error on 403")
	}
}

func TestFetchToken_RetriesThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"value":"tok-after-retry"}`))
	}))
	defer srv.Close()
	g := &GithubOIDC{requestURL: srv.URL, requestToken: "x", client: srv.Client()}
	tok, err := g.FetchToken("aud")
	if err != nil {
		t.Fatalf("FetchToken should succeed after retries: %v", err)
	}
	if tok != "tok-after-retry" {
		t.Errorf("token = %q", tok)
	}
	if calls != 3 {
		t.Errorf("server hit %d times, want 3", calls)
	}
}
