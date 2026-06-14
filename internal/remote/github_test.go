package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Compile-time check: GitHubClient satisfies the Backend interface.
var _ Backend = (*GitHubClient)(nil)

// newTestClient builds a GitHubClient pointed at the given test server URL.
func newTestClient(serverURL string) *GitHubClient {
	return &GitHubClient{
		Owner:   "alice",
		Repo:    "data",
		Path:    "portfolio.fin",
		Branch:  "main",
		Token:   "ghp_testtoken",
		HTTP:    &http.Client{},
		BaseURL: serverURL,
	}
}

// --- Fetch tests ---

func TestFetchReturnsContentAndSHA(t *testing.T) {
	const content = "hello finador"
	const sha = "abc123sha"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify URL path and query
		if !strings.HasSuffix(r.URL.Path, "/contents/portfolio.fin") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("ref") != "main" {
			t.Errorf("unexpected ref query param: %s", r.URL.Query().Get("ref"))
		}
		// Verify Bearer token
		auth := r.Header.Get("Authorization")
		if auth != "Bearer ghp_testtoken" {
			t.Errorf("unexpected Authorization: %s", auth)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content": base64.StdEncoding.EncodeToString([]byte(content)),
			"sha":     sha,
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	data, v, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != content {
		t.Errorf("data: got %q, want %q", data, content)
	}
	if string(v) != sha {
		t.Errorf("version: got %q, want %q", v, sha)
	}
}

func TestFetchBase64WithNewlines(t *testing.T) {
	// GitHub wraps base64 at 60 chars with \n — we must handle that.
	const content = "some binary data that results in multi-line base64 encoding"
	raw := base64.StdEncoding.EncodeToString([]byte(content))
	// Insert newlines every 10 chars to simulate GitHub's wrapping.
	var wrapped strings.Builder
	for i, ch := range raw {
		if i > 0 && i%10 == 0 {
			wrapped.WriteByte('\n')
		}
		wrapped.WriteRune(ch)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"content": wrapped.String(),
			"sha":     "sha1",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	data, _, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != content {
		t.Errorf("data after newline-stripping: got %q, want %q", data, content)
	}
}

func TestFetch404ReturnsErrRemoteMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrRemoteMissing) {
		t.Errorf("expected ErrRemoteMissing, got %v", err)
	}
}

func TestFetch401ReturnsErrRemoteAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrRemoteAuth) {
		t.Errorf("expected ErrRemoteAuth, got %v", err)
	}
}

func TestFetch403ReturnsErrRemoteAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, _, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrRemoteAuth) {
		t.Errorf("expected ErrRemoteAuth, got %v", err)
	}
}

func TestFetchOfflineReturnsErrOffline(t *testing.T) {
	// Point at a server that is closed immediately — transport will error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close before the request

	c := newTestClient(srv.URL)
	_, _, err := c.Fetch(context.Background())
	if !errors.Is(err, ErrOffline) {
		t.Errorf("expected ErrOffline, got %v", err)
	}
}

// --- Push tests ---

func TestPushSendsBase64ContentAndSHA(t *testing.T) {
	const fileContent = "my encrypted data"
	const baseSHA = "base-sha-123"
	const newSHA = "new-sha-456"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		// Verify Bearer token
		auth := r.Header.Get("Authorization")
		if auth != "Bearer ghp_testtoken" {
			t.Errorf("unexpected Authorization: %s", auth)
		}
		var body pushRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// Verify content is base64 of fileContent
		decoded, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			t.Fatalf("decode base64 content: %v", err)
		}
		if string(decoded) != fileContent {
			t.Errorf("content: got %q, want %q", decoded, fileContent)
		}
		if body.SHA != baseSHA {
			t.Errorf("sha: got %q, want %q", body.SHA, baseSHA)
		}
		if body.Branch != "main" {
			t.Errorf("branch: got %q, want \"main\"", body.Branch)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": newSHA},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	v, err := c.Push(context.Background(), []byte(fileContent), Version(baseSHA), "test commit")
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if string(v) != newSHA {
		t.Errorf("returned version: got %q, want %q", v, newSHA)
	}
}

func TestPushNewFileOmitsSHA(t *testing.T) {
	// When base == "", the sha field must be omitted (not sent as empty string).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, ok := raw["sha"]; ok {
			t.Error("sha should be omitted when base is empty")
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "created-sha"},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	v, err := c.Push(context.Background(), []byte("data"), Version(""), "init commit")
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if string(v) != "created-sha" {
		t.Errorf("version: got %q, want \"created-sha\"", v)
	}
}

func TestPush409ReturnsErrRemoteConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Push(context.Background(), []byte("data"), Version("old-sha"), "msg")
	if !errors.Is(err, ErrRemoteConflict) {
		t.Errorf("expected ErrRemoteConflict, got %v", err)
	}
}

func TestPush422ReturnsErrRemoteConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Push(context.Background(), []byte("data"), Version("stale"), "msg")
	if !errors.Is(err, ErrRemoteConflict) {
		t.Errorf("expected ErrRemoteConflict, got %v", err)
	}
}

func TestPush401ReturnsErrRemoteAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Push(context.Background(), []byte("data"), Version("sha"), "msg")
	if !errors.Is(err, ErrRemoteAuth) {
		t.Errorf("expected ErrRemoteAuth, got %v", err)
	}
}

func TestPushOfflineReturnsErrOffline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.Push(context.Background(), []byte("data"), Version("sha"), "msg")
	if !errors.Is(err, ErrOffline) {
		t.Errorf("expected ErrOffline, got %v", err)
	}
}

// --- Describe ---

func TestDescribe(t *testing.T) {
	c := &GitHubClient{Owner: "alice", Repo: "data", Path: "portfolio.fin", Branch: "main"}
	got := c.Describe()
	want := "github:alice/data/portfolio.fin@main"
	if got != want {
		t.Errorf("Describe: got %q, want %q", got, want)
	}
}

// --- NewGitHub ---

func TestNewGitHubDefaultsBranch(t *testing.T) {
	gh := GitHub{Owner: "a", Repo: "b", Path: "c.fin"}
	c := NewGitHub(gh, "tok")
	if c.Branch != "main" {
		t.Errorf("Branch: got %q, want \"main\"", c.Branch)
	}
}

func TestNewGitHubPreservesBranch(t *testing.T) {
	gh := GitHub{Owner: "a", Repo: "b", Path: "c.fin", Branch: "prod"}
	c := NewGitHub(gh, "tok")
	if c.Branch != "prod" {
		t.Errorf("Branch: got %q, want \"prod\"", c.Branch)
	}
}

// --- Retry behaviour ---

func TestFetchRetriesOn5xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content": base64.StdEncoding.EncodeToString([]byte("ok")),
			"sha":     "sha1",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	// Override retryWait would require exporting it; for this test we just
	// verify that the second attempt succeeds (the default 500ms is fine in CI).
	data, _, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch after retry: %v", err)
	}
	if string(data) != "ok" {
		t.Errorf("data: got %q", data)
	}
	if attempts != 2 {
		t.Errorf("attempts: got %d, want 2", attempts)
	}
}

func TestCheckAccess(t *testing.T) {
	// 200 → the token can reach the repo.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/repos/alice/data") {
			t.Errorf("CheckAccess hit %s, want the repo-metadata endpoint", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/data"}`))
	}))
	defer ok.Close()
	if err := newTestClient(ok.URL).CheckAccess(context.Background()); err != nil {
		t.Errorf("CheckAccess(200) = %v, want nil", err)
	}

	// 404 → a private repo the token can't see is reported as an auth problem.
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	if err := newTestClient(missing.URL).CheckAccess(context.Background()); !errors.Is(err, ErrRemoteAuth) {
		t.Errorf("CheckAccess(404) = %v, want ErrRemoteAuth", err)
	}
}
