// Package gh implements a minimal GitHub App client: JWT-based app auth,
// the Checks API, and PR comment upsert. Only what Parity needs (spec
// 6.9), not a general GitHub SDK. See docs/plans/phase-4.md: this is
// built and unit tested against a fake GitHub API, but has never
// authenticated against the real api.github.com (no App credentials in
// this dev environment).
package gh

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Client talks to the GitHub REST API (or a fake server in tests, via
// BaseURL) using an already-obtained installation access token.
type Client struct {
	BaseURL string // e.g. "https://api.github.com"; overridable in tests
	Token   string
	HTTP    *http.Client
}

// New creates a Client for api.github.com using an installation token.
func New(token string) *Client {
	return &Client{BaseURL: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// SignAppJWT builds the short-lived JSON Web Token a GitHub App uses to
// authenticate as itself (before exchanging for an installation token),
// per GitHub's documented App-auth flow. appID is the App's numeric ID;
// key is the App's private key.
func SignAppJWT(appID string, key *rsa.PrivateKey) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-30 * time.Second).Unix(), // allow for clock drift, per GitHub's docs
		"exp": now.Add(9 * time.Minute).Unix(),    // GitHub caps this at 10 minutes
		"iss": appID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

// CheckRunConclusion mirrors the GitHub Checks API conclusion enum.
type CheckRunConclusion string

const (
	ConclusionSuccess CheckRunConclusion = "success"
	ConclusionNeutral CheckRunConclusion = "neutral"
	ConclusionFailure CheckRunConclusion = "failure"
)

// CheckRun is the subset of the Checks API payload Parity sets.
type CheckRun struct {
	Name       string             `json:"name"`
	HeadSHA    string             `json:"head_sha"`
	Status     string             `json:"status"` // "completed"
	Conclusion CheckRunConclusion `json:"conclusion"`
	Output     CheckRunOutput     `json:"output"`
}

type CheckRunOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// CreateCheckRun posts a new check run to owner/repo, per the Checks API.
func (c *Client) CreateCheckRun(ctx context.Context, owner, repo string, run CheckRun) error {
	url := fmt.Sprintf("%s/repos/%s/%s/check-runs", c.BaseURL, owner, repo)
	return c.doJSON(ctx, http.MethodPost, url, run, nil)
}

// Comment is a PR issue-comment body.
type Comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// commentMarker identifies Parity's own comment on a PR so UpsertComment
// can find and update it instead of spamming a new comment every run
// (spec 6.9: "updated in place, not spammed").
const commentMarker = "<!-- parity-report -->"

// UpsertComment creates or updates Parity's PR comment on issue prNumber,
// finding the existing one (if any) via commentMarker.
func (c *Client) UpsertComment(ctx context.Context, owner, repo string, prNumber int, body string) error {
	full := commentMarker + "\n" + body

	listURL := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", c.BaseURL, owner, repo, prNumber)
	var comments []Comment
	if err := c.doJSON(ctx, http.MethodGet, listURL, nil, &comments); err != nil {
		return fmt.Errorf("gh: list comments: %w", err)
	}

	for _, existing := range comments {
		if hasMarker(existing.Body) {
			updateURL := fmt.Sprintf("%s/repos/%s/%s/issues/comments/%d", c.BaseURL, owner, repo, existing.ID)
			return c.doJSON(ctx, http.MethodPatch, updateURL, Comment{Body: full}, nil)
		}
	}
	return c.doJSON(ctx, http.MethodPost, listURL, Comment{Body: full}, nil)
}

func hasMarker(body string) bool {
	return len(body) >= len(commentMarker) && body[:len(commentMarker)] == commentMarker
}

func (c *Client) doJSON(ctx context.Context, method, url string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gh: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("gh: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("gh: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gh: %s %s: status %d: %s", method, url, resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("gh: decode response: %w", err)
		}
	}
	return nil
}
