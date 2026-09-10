package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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
	// GitHub wraps base64 at 60 chars with \n - we must handle that.
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
	// Point at a server that is closed immediately - transport will error.
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
	if c.Branch != "master" {
		t.Errorf("Branch: got %q, want \"master\"", c.Branch)
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

// --- Non-2xx statuses the API can answer with ---

// Any status the switch does not name is reported verbatim (method, code and
// the trimmed body), so an unexpected API answer is diagnosable instead of
// silently becoming "offline".
func TestUnexpectedStatusIsReported(t *testing.T) {
	cases := []struct {
		name string
		call func(*GitHubClient) error
		want string
	}{
		{"fetch", func(c *GitHubClient) error { _, _, err := c.Fetch(context.Background()); return err }, "github fetch: HTTP 451"},
		{"push", func(c *GitHubClient) error {
			_, err := c.Push(context.Background(), []byte("d"), Version("s"), "m")
			return err
		}, "github push: HTTP 451"},
		{"check access", func(c *GitHubClient) error { return c.CheckAccess(context.Background()) }, "github repo check: HTTP 451"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnavailableForLegalReasons)
				_, _ = w.Write([]byte("  blocked  \n"))
			}))
			defer srv.Close()
			err := c.call(newTestClient(srv.URL))
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "blocked") {
				t.Errorf("error = %v, want %q and the body", err, c.want)
			}
		})
	}
}

// A 200 whose body is not the documented shape is an error, never an empty
// file: decoding garbage as "no content" would push an empty ledger next.
func TestMalformedResponsesAreErrors(t *testing.T) {
	cases := []struct {
		name, body, want string
		push             bool
	}{
		{name: "fetch: not JSON", body: "<html>nope</html>", want: "decode response"},
		{name: "fetch: content is not base64", body: `{"content":"!!!not-base64!!!","sha":"s"}`, want: "base64 decode"},
		{name: "push: not JSON", body: "<html>nope</html>", want: "decode response", push: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			cl := newTestClient(srv.URL)
			var err error
			if c.push {
				_, err = cl.Push(context.Background(), []byte("d"), Version(""), "m")
			} else {
				_, _, err = cl.Fetch(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want %q", err, c.want)
			}
		})
	}
}

// A throttled push is retried with the same body: the PUT must be rebuilt, not
// replayed from a drained reader, or the second attempt would upload nothing.
func TestPushRetriesOn429WithTheSameBody(t *testing.T) {
	const content = "line one\nline two\n"
	var bodies []string
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body pushRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		decoded, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			t.Fatalf("decode base64: %v", err)
		}
		bodies = append(bodies, string(decoded))
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": "s2"}})
	}))
	defer srv.Close()

	v, err := newTestClient(srv.URL).Push(context.Background(), []byte(content), Version("s1"), "msg")
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if v != "s2" || attempts != 2 {
		t.Errorf("version %q after %d attempts, want s2 after 2", v, attempts)
	}
	for i, b := range bodies {
		if b != content {
			t.Errorf("attempt %d uploaded %q, want the file bytes unchanged", i+1, b)
		}
	}
}

// The bytes on the remote are exactly the bytes handed to Push: no
// re-encoding, no trailing-newline fixing. Byte-stability is what keeps the
// git diffs small and the hash chain intact.
func TestPushIsByteStable(t *testing.T) {
	data := []byte{0x00, 0x01, 'f', 'i', 'n', 0xff, '\n', '\n'}
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body pushRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		got, _ = base64.StdEncoding.DecodeString(body.Content)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": "s"}})
	}))
	defer srv.Close()
	if _, err := newTestClient(srv.URL).Push(context.Background(), data, Version(""), "m"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("uploaded % x, want % x", got, data)
	}
}

// A cancelled context during the retry backoff stops the call instead of
// sleeping on: the caller has gone away.
func TestRetryStopsOnCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := newTestClient(srv.URL)
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, _, err := c.Fetch(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Fetch with a cancelled context = %v, want context.Canceled", err)
	}
}

// The token authenticates every call - and must never reach a log line, an
// error message or the remote's own identifier, all of which end up on screen
// or in a support paste.
func TestTokenNeverLeaks(t *testing.T) {
	const token = "ghp_supersecret_TOKEN"
	seen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization") == "Bearer "+token
		w.WriteHeader(http.StatusInternalServerError) // force the error-message path
		_, _ = w.Write([]byte("server said no"))
	}))
	defer srv.Close()

	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	c := newTestClient(srv.URL)
	c.Token = token
	_, _, ferr := c.Fetch(context.Background())
	_, perr := c.Push(context.Background(), []byte("d"), Version("s"), "m")
	aerr := c.CheckAccess(context.Background())
	if !seen {
		t.Fatal("the token never reached the Authorization header")
	}
	for _, s := range []string{fmt.Sprint(ferr), fmt.Sprint(perr), fmt.Sprint(aerr),
		c.Describe(), logged.String()} {
		if strings.Contains(s, token) {
			t.Errorf("the token leaked into %q", s)
		}
	}
}

// A client built by hand (no BaseURL) still talks to GitHub itself: the two
// endpoint builders fall back to the public API, they never produce a
// relative URL.
func TestEndpointsDefaultToTheGitHubAPI(t *testing.T) {
	c := &GitHubClient{Owner: "alice", Repo: "data", Path: "dir/portfolio.fin", Branch: "main"}
	if got, want := c.contentsURL(), defaultBaseURL+"/repos/alice/data/contents/dir/portfolio.fin"; got != want {
		t.Errorf("contentsURL = %q, want %q", got, want)
	}
	if got, want := c.repoURL(), defaultBaseURL+"/repos/alice/data"; got != want {
		t.Errorf("repoURL = %q, want %q", got, want)
	}
}
