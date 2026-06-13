package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL     = "https://api.github.com"
	githubAPIVersion   = "2022-11-28"
	defaultHTTPTimeout = 15 * time.Second
	retryWait          = 500 * time.Millisecond
)

// GitHubClient implements Backend via the GitHub Contents API.
// It requires no external dependencies beyond net/http.
type GitHubClient struct {
	Owner, Repo, Path, Branch, Token string
	HTTP                             *http.Client
	BaseURL                          string // default "https://api.github.com"; tests override
}

// NewGitHub creates a GitHubClient from a Config GitHub section and a PAT.
// Branch defaults to "main" when empty.
func NewGitHub(gh GitHub, token string) *GitHubClient {
	branch := gh.Branch
	if branch == "" {
		branch = "main"
	}
	return &GitHubClient{
		Owner:   gh.Owner,
		Repo:    gh.Repo,
		Path:    gh.Path,
		Branch:  branch,
		Token:   token,
		HTTP:    &http.Client{Timeout: defaultHTTPTimeout},
		BaseURL: defaultBaseURL,
	}
}

// Describe returns a human-readable identifier for this remote.
func (g *GitHubClient) Describe() string {
	return fmt.Sprintf("github:%s/%s/%s@%s", g.Owner, g.Repo, g.Path, g.Branch)
}

// contentsURL returns the API URL for this client's file.
func (g *GitHubClient) contentsURL() string {
	base := g.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return fmt.Sprintf("%s/repos/%s/%s/contents/%s", base, g.Owner, g.Repo, g.Path)
}

// setHeaders adds the required GitHub API request headers.
func (g *GitHubClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
}

// doWithRetry executes req and retries once on 5xx or 429 with a short backoff.
// On transport errors (no response), it returns ErrOffline.
func (g *GitHubClient) doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) (*http.Response, error) {
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	for attempt := 0; ; attempt++ {
		req, err := buildReq()
		if err != nil {
			return nil, err
		}
		g.setHeaders(req)

		resp, err := client.Do(req)
		if err != nil {
			return nil, ErrOffline
		}

		retriable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retriable && attempt < 1 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryWait):
			}
			continue
		}
		return resp, nil
	}
}

// fetchResponse is the JSON shape returned by the Contents GET endpoint.
type fetchResponse struct {
	Content string `json:"content"` // base64, may contain \n
	SHA     string `json:"sha"`
}

// Fetch downloads the file from GitHub and returns its decoded content and
// current SHA as Version. Returns ErrRemoteMissing on 404, ErrRemoteAuth on
// 401/403, ErrOffline on transport errors, and a descriptive error for other
// non-2xx statuses.
func (g *GitHubClient) Fetch(ctx context.Context) ([]byte, Version, error) {
	url := g.contentsURL() + "?ref=" + g.Branch

	resp, err := g.doWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// handled below
	case http.StatusNotFound:
		return nil, "", ErrRemoteMissing
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, "", ErrRemoteAuth
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("github fetch: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var fr fetchResponse
	if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
		return nil, "", fmt.Errorf("github fetch: decode response: %w", err)
	}

	// The base64 content may contain newlines; strip whitespace before decoding.
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, fr.Content)
	data, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, "", fmt.Errorf("github fetch: base64 decode: %w", err)
	}
	return data, Version(fr.SHA), nil
}

// pushRequest is the JSON body for the Contents PUT endpoint.
type pushRequest struct {
	Message string `json:"message"`
	Content string `json:"content"` // base64-encoded
	Branch  string `json:"branch"`
	SHA     string `json:"sha,omitempty"` // omitted when creating a new file
}

// pushResponse is the subset of the PUT response we care about.
type pushResponse struct {
	Content struct {
		SHA string `json:"sha"`
	} `json:"content"`
}

// Push uploads data to GitHub as a new commit with the given message. base
// must be the Version from the last Fetch; pass Version("") to create a new
// file. Returns the new SHA as Version. Returns ErrRemoteConflict on 409/422,
// ErrRemoteAuth on 401/403, ErrOffline on transport errors.
func (g *GitHubClient) Push(ctx context.Context, data []byte, base Version, message string) (Version, error) {
	url := g.contentsURL()

	body := pushRequest{
		Message: message,
		Content: base64.StdEncoding.EncodeToString(data),
		Branch:  g.Branch,
		SHA:     string(base),
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("github push: marshal request: %w", err)
	}

	resp, err := g.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// handled below
	case http.StatusConflict, http.StatusUnprocessableEntity:
		return "", ErrRemoteConflict
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrRemoteAuth
	default:
		body2, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("github push: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body2)))
	}

	var pr pushResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", fmt.Errorf("github push: decode response: %w", err)
	}
	return Version(pr.Content.SHA), nil
}
