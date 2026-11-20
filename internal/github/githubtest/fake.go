// Package githubtest is a fake GitHub REST API for tests: pull requests
// with paginated changed files and patches, conversation comments (list,
// create, update), the token's user, and injected 401/403/404/429 answers.
// It records every request so tests can assert, for example, that a dry run
// never posted a comment.
package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// File is a changed file of a fake pull request; Patch nil means GitHub
// returned no patch (binary or too large).
type File struct {
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	PreviousFilename string  `json:"previous_filename,omitempty"`
	Patch            *string `json:"patch,omitempty"`
}

// PullRequest is a fake pull request.
type PullRequest struct {
	Number   int
	Title    string
	State    string
	BaseRef  string
	BaseSHA  string
	HeadRef  string
	HeadSHA  string
	HeadRepo string // OWNER/REPO of the head repository (a fork when different)
	Files    []File
	// ChangedFiles overrides the reported count (0: len(Files)).
	ChangedFiles int
}

// Comment is a stored conversation comment.
type Comment struct {
	ID     int64
	Body   string
	Author string
}

// Request is one recorded request.
type Request struct {
	Method, Path string
	// Authorized: the request carried "Authorization: Bearer <Token>" (or a
	// token of Users).
	Authorized bool
	// Login is the account of the token the request carried ("" anonymous).
	Login string
}

// Server is the fake API. Token is the accepted bearer token ("" accepts
// anonymous calls only); Login is the token's account.
type Server struct {
	*httptest.Server
	Token string
	Login string
	// Users are more accepted tokens and their accounts (token -> login).
	Users map[string]string
	// Private repositories ("owner/repo" -> the only login that may read
	// it): their pull requests are 404 to anyone else, as GitHub answers.
	Private map[string]string
	// PerPage is the files and comments page size (default 100).
	PerPage int
	// Fail answers every request whose path has this prefix with FailStatus
	// (429 and 403 with "rate limit" set the rate-limit headers).
	FailPrefix string
	FailStatus int
	FailBody   string

	mu       sync.Mutex
	pulls    map[string]*PullRequest // "owner/repo#n"
	comments map[string][]*Comment   // "owner/repo#n"
	nextID   int64
	requests []Request
}

// New starts a fake API.
func New(token, login string) *Server {
	s := &Server{Token: token, Login: login, pulls: map[string]*PullRequest{}, comments: map[string][]*Comment{}, nextID: 1000}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func key(repo string, n int) string { return strings.ToLower(repo) + "#" + strconv.Itoa(n) }

// AddPullRequest registers pr in repo (OWNER/REPO).
func (s *Server) AddPullRequest(repo string, pr PullRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := pr
	s.pulls[key(repo, pr.Number)] = &c
}

// SetHead moves a pull request's head commit (a new push).
func (s *Server) SetHead(repo string, n int, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.pulls[key(repo, n)]; p != nil {
		p.HeadSHA = sha
	}
}

// AddComment stores a comment written by author.
func (s *Server) AddComment(repo string, n int, author, body string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.comments[key(repo, n)] = append(s.comments[key(repo, n)], &Comment{ID: s.nextID, Body: body, Author: author})
	return s.nextID
}

// Comments returns a copy of a pull request's comments.
func (s *Server) Comments(repo string, n int) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Comment
	for _, c := range s.comments[key(repo, n)] {
		out = append(out, *c)
	}
	return out
}

// Requests returns the recorded requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Writes counts recorded POST and PATCH requests.
func (s *Server) Writes() int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			n++
		}
	}
	return n
}

func (s *Server) perPage(r *http.Request) int {
	n := s.PerPage
	if n <= 0 {
		n = 100
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && v > 0 && v < n {
		n = v
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// loginOf is the account of the request's bearer token ("" for none or
// an unknown token).
func (s *Server) loginOf(auth string) string {
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || token == "" {
		return ""
	}
	if s.Token != "" && token == s.Token {
		return s.Login
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Users[token]
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	login := s.loginOf(auth)
	authorized := login != ""
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Authorized: authorized, Login: login})
	s.mu.Unlock()
	if s.FailPrefix != "" && strings.HasPrefix(r.URL.Path, s.FailPrefix) {
		if s.FailStatus == http.StatusTooManyRequests || strings.Contains(s.FailBody, "rate limit") {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", "60")
		}
		writeJSON(w, s.FailStatus, map[string]string{"message": s.FailBody})
		return
	}
	if auth != "" && !authorized {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/user":
		if !authorized {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Requires authentication"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"login": login, "id": 42})
	case len(parts) >= 5 && parts[0] == "repos" && parts[3] == "pulls":
		s.servePull(w, r, parts[1]+"/"+parts[2], parts[4:], login)
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "issues" && parts[5] == "comments":
		n, _ := strconv.Atoi(parts[4])
		s.serveComments(w, r, parts[1]+"/"+parts[2], n, login)
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "issues" && parts[4] == "comments" && r.Method == http.MethodPatch:
		id, _ := strconv.ParseInt(parts[5], 10, 64)
		s.updateComment(w, r, parts[1]+"/"+parts[2], id, login)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func (s *Server) servePull(w http.ResponseWriter, r *http.Request, repo string, rest []string, login string) {
	n, _ := strconv.Atoi(rest[0])
	s.mu.Lock()
	p := s.pulls[key(repo, n)]
	var pr PullRequest
	if p != nil {
		pr = *p
	}
	s.mu.Unlock()
	s.mu.Lock()
	reader, private := s.Private[strings.ToLower(repo)]
	s.mu.Unlock()
	if p == nil || r.Method != http.MethodGet || (private && reader != login) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if len(rest) == 1 {
		count := pr.ChangedFiles
		if count == 0 {
			count = len(pr.Files)
		}
		headRepo := pr.HeadRepo
		if headRepo == "" {
			headRepo = repo
		}
		state := pr.State
		if state == "" {
			state = "open"
		}
		writeJSON(w, http.StatusOK, map[string]any{"number": pr.Number, "state": state, "title": pr.Title, "draft": false, "changed_files": count,
			"base": map[string]any{"ref": pr.BaseRef, "sha": pr.BaseSHA, "repo": map[string]string{"full_name": repo}},
			"head": map[string]any{"ref": pr.HeadRef, "sha": pr.HeadSHA, "repo": map[string]string{"full_name": headRepo}}})
		return
	}
	if len(rest) == 2 && rest[1] == "files" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		page = max(page, 1)
		size := s.perPage(r)
		from := (page - 1) * size
		to := min(from+size, len(pr.Files))
		batch := []File{}
		if from < len(pr.Files) {
			batch = pr.Files[from:to]
		}
		if to < len(pr.Files) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=%d&page=%d>; rel="next"`, s.URL, r.URL.Path, size, page+1))
		}
		writeJSON(w, http.StatusOK, batch)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func (s *Server) commentJSON(repo string, c *Comment) map[string]any {
	return map[string]any{"id": c.ID, "body": c.Body, "html_url": fmt.Sprintf("https://github.com/%s/pull/0#issuecomment-%d", repo, c.ID), "user": map[string]any{"login": c.Author, "id": 7}}
}

func (s *Server) serveComments(w http.ResponseWriter, r *http.Request, repo string, n int, login string) {
	authorized := login != ""
	s.mu.Lock()
	_, known := s.pulls[key(repo, n)]
	s.mu.Unlock()
	if !known {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		all := s.comments[key(repo, n)]
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		page = max(page, 1)
		size := s.perPage(r)
		from := (page - 1) * size
		to := min(from+size, len(all))
		out := []map[string]any{}
		for i := from; i < to; i++ {
			out = append(out, s.commentJSON(repo, all[i]))
		}
		s.mu.Unlock()
		if to < len(all) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=%d&page=%d>; rel="next"`, s.URL, r.URL.Path, size, page+1))
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		if !authorized {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Requires authentication"})
			return
		}
		var in struct {
			Body string `json:"body"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "Validation Failed"})
			return
		}
		s.mu.Lock()
		s.nextID++
		c := &Comment{ID: s.nextID, Body: in.Body, Author: login}
		s.comments[key(repo, n)] = append(s.comments[key(repo, n)], c)
		out := s.commentJSON(repo, c)
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, out)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func (s *Server) updateComment(w http.ResponseWriter, r *http.Request, repo string, id int64, login string) {
	authorized := login != ""
	if !authorized {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Requires authentication"})
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, list := range s.comments {
		if !strings.HasPrefix(k, strings.ToLower(repo)+"#") {
			continue
		}
		for _, c := range list {
			if c.ID == id {
				if c.Author != login {
					writeJSON(w, http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
					return
				}
				c.Body = in.Body
				writeJSON(w, http.StatusOK, s.commentJSON(repo, c))
				return
			}
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}
