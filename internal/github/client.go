// Package github is CODEYE's GitHub REST client for the service
// (docs/GITHUB.md): the token's own account, a pull request's metadata and
// changed files with their patches, and the one CODEYE conversation comment
// of a pull request (list, create, update). It never follows a redirect or a
// pagination link to another host, so the token is sent to the configured
// API host only, and it keeps no response text beyond what callers ask for.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIURL is the REST API of github.com.
const DefaultAPIURL = "https://api.github.com"

// Host is the Git host whose API DefaultAPIURL is.
const Host = "github.com"

// Error kinds (Error.Kind). They are reason-code shaped so they can be
// recorded as run reasons.
const (
	KindUnauthorized = "github_unauthorized" // 401: the token is not accepted
	KindForbidden    = "github_forbidden"    // 403 without a rate limit: no permission
	KindNotFound     = "github_not_found"    // 404: no such resource, or not visible to the token
	KindRateLimited  = "github_rate_limited" // 403/429 with an exhausted limit
	KindUnavailable  = "github_unavailable"  // network failure or 5xx
	KindInvalid      = "github_response_invalid"
	KindTooLarge     = "github_body_too_large" // 422 about the body size
)

// Error is a failed API call. It never holds the token or a response body.
type Error struct {
	Kind   string
	Status int
	// RetryAfter is when GitHub says to try again (rate limits), 0 if unknown.
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s (HTTP %d)", e.Kind, e.Status)
	}
	return e.Kind
}

// KindOf is the Error kind of err, or KindUnavailable for any other error.
func KindOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnavailable
}

// Client calls the REST API at BaseURL with Token (anonymous when empty).
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// UserAgent identifies the client; GitHub requires one.
	UserAgent string
}

// New is a client of base (DefaultAPIURL when empty) with token.
func New(base, token string) *Client {
	if base == "" {
		base = DefaultAPIURL
	}
	return &Client{BaseURL: strings.TrimSuffix(base, "/"), Token: token}
}

var defaultHTTP = http.Client{Timeout: 30 * time.Second}

func (c *Client) http() *http.Client {
	h := defaultHTTP
	if c.HTTP != nil {
		h = *c.HTTP
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &h
}

// maxResponse bounds one response body.
const maxResponse = 8 << 20

// do sends one request to path (relative to BaseURL, or an absolute URL on
// the BaseURL host for pagination) and decodes a 2xx JSON answer into out.
// It returns the response header.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) (http.Header, error) {
	target := path
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		target = c.BaseURL + path
	}
	if !sameOrigin(c.BaseURL, target) {
		return nil, &Error{Kind: KindInvalid}
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, &Error{Kind: KindInvalid}
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, &Error{Kind: KindInvalid}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	ua := c.UserAgent
	if ua == "" {
		ua = "codeye"
	}
	req.Header.Set("User-Agent", ua)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, &Error{Kind: KindUnavailable}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, &Error{Kind: KindUnavailable, Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.Header, statusError(resp, raw)
	}
	if len(raw) > maxResponse {
		return nil, &Error{Kind: KindInvalid, Status: resp.StatusCode}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return nil, &Error{Kind: KindInvalid, Status: resp.StatusCode}
	}
	return resp.Header, nil
}

func statusError(resp *http.Response, body []byte) error {
	e := &Error{Status: resp.StatusCode}
	limited := resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && bytes.Contains(bytes.ToLower(body), []byte("rate limit")))
	switch {
	case limited:
		e.Kind = KindRateLimited
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			e.RetryAfter = time.Duration(s) * time.Second
		} else if t, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			if d := time.Until(time.Unix(t, 0)); d > 0 {
				e.RetryAfter = d
			}
		}
	case resp.StatusCode == http.StatusUnauthorized:
		e.Kind = KindUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		e.Kind = KindForbidden
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		e.Kind = KindNotFound
	case resp.StatusCode == http.StatusUnprocessableEntity && bytes.Contains(bytes.ToLower(body), []byte("too long")):
		e.Kind = KindTooLarge
	case resp.StatusCode >= 500:
		e.Kind = KindUnavailable
	default:
		e.Kind = KindInvalid
	}
	return e
}

// sameOrigin reports whether target has the scheme and host of base.
func sameOrigin(base, target string) bool {
	b, err1 := url.Parse(base)
	t, err2 := url.Parse(target)
	return err1 == nil && err2 == nil && b.Scheme == t.Scheme && strings.EqualFold(b.Host, t.Host) && t.User == nil
}

// nextLink is the rel="next" URL of a Link header, "" when none.
func nextLink(h http.Header) string {
	for _, part := range strings.Split(h.Get("Link"), ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if !ok || !strings.Contains(params, `rel="next"`) {
			continue
		}
		return strings.Trim(strings.TrimSpace(target), "<>")
	}
	return ""
}

// User is the account a token belongs to.
type User struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// User returns the token's own account (GET /user).
func (c *Client) User(ctx context.Context) (User, error) {
	var u User
	if _, err := c.do(ctx, http.MethodGet, "/user", nil, &u); err != nil {
		return User{}, err
	}
	if u.Login == "" {
		return User{}, &Error{Kind: KindInvalid}
	}
	return u, nil
}

// PullRequestInfo is the part of GET /repos/{o}/{r}/pulls/{n} CODEYE reads.
type PullRequestInfo struct {
	Number       int    `json:"number"`
	State        string `json:"state"`
	Title        string `json:"title"`
	Draft        bool   `json:"draft"`
	ChangedFiles int    `json:"changed_files"`
	Base         struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

func repoPath(repository string) string {
	owner, name, _ := strings.Cut(repository, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// PullRequest reads a pull request's metadata.
func (c *Client) PullRequest(ctx context.Context, repository string, number int) (PullRequestInfo, error) {
	var pr PullRequestInfo
	_, err := c.do(ctx, http.MethodGet, repoPath(repository)+"/pulls/"+strconv.Itoa(number), nil, &pr)
	return pr, err
}

// FileInfo is one entry of GET /repos/{o}/{r}/pulls/{n}/files. Patch is
// absent for binary files and diffs GitHub does not show.
type FileInfo struct {
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	PreviousFilename string  `json:"previous_filename"`
	Patch            *string `json:"patch"`
}

// maxFilePages bounds the files listing: GitHub lists at most 3000 files of
// a pull request, 100 per page.
const maxFilePages = 30

// PullRequestFiles lists every changed file GitHub reports, following the
// pagination links.
func (c *Client) PullRequestFiles(ctx context.Context, repository string, number int) ([]FileInfo, error) {
	var out []FileInfo
	next := repoPath(repository) + "/pulls/" + strconv.Itoa(number) + "/files?per_page=100"
	for page := 0; next != "" && page < maxFilePages; page++ {
		var batch []FileInfo
		h, err := c.do(ctx, http.MethodGet, next, nil, &batch)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		next = nextLink(h)
	}
	return out, nil
}

// Comment is a pull request's conversation (issue) comment.
type Comment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    *User  `json:"user"`
}

// maxCommentPages bounds the comment listing (100 per page).
const maxCommentPages = 50

// Comments lists the conversation comments of a pull request.
func (c *Client) Comments(ctx context.Context, repository string, number int) ([]Comment, error) {
	var out []Comment
	next := repoPath(repository) + "/issues/" + strconv.Itoa(number) + "/comments?per_page=100"
	for page := 0; next != "" && page < maxCommentPages; page++ {
		var batch []Comment
		h, err := c.do(ctx, http.MethodGet, next, nil, &batch)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		next = nextLink(h)
	}
	return out, nil
}

// CreateComment adds a conversation comment to a pull request.
func (c *Client) CreateComment(ctx context.Context, repository string, number int, body string) (Comment, error) {
	var out Comment
	_, err := c.do(ctx, http.MethodPost, repoPath(repository)+"/issues/"+strconv.Itoa(number)+"/comments", map[string]string{"body": body}, &out)
	return out, err
}

// UpdateComment replaces the body of a conversation comment.
func (c *Client) UpdateComment(ctx context.Context, repository string, id int64, body string) (Comment, error) {
	var out Comment
	_, err := c.do(ctx, http.MethodPatch, repoPath(repository)+"/issues/comments/"+strconv.FormatInt(id, 10), map[string]string{"body": body}, &out)
	return out, err
}
