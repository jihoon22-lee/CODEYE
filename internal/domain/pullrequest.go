package domain

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// PullRequestRef is the pull_request object of a run request: the number of
// a pull request of the project's GitHub repository and, optionally, this
// run's own answer to "post the result as a pull-request comment" (absent:
// the project's remembered choice). The server resolves it into a
// PullRequest pin before the run is queued (docs/GITHUB.md).
type PullRequestRef struct {
	Number  int   `json:"number"`
	Comment *bool `json:"comment,omitempty"`
}

// MaxPullRequestNumber bounds pull-request numbers (GitHub numbers are
// positive 32-bit integers).
const MaxPullRequestNumber = 1<<31 - 1

// Validate checks a pull-request reference of a run request.
func (r PullRequestRef) Validate() error {
	if r.Number < 1 || r.Number > MaxPullRequestNumber {
		return invalidf("pull_request.number must be a positive integer")
	}
	return nil
}

// Pull-request file statuses as GitHub reports them.
const (
	FileAdded     = "added"
	FileModified  = "modified"
	FileRemoved   = "removed"
	FileRenamed   = "renamed"
	FileCopied    = "copied"
	FileChanged   = "changed"
	FileUnchanged = "unchanged"
)

var fileStatuses = map[string]bool{FileAdded: true, FileModified: true, FileRemoved: true, FileRenamed: true, FileCopied: true, FileChanged: true, FileUnchanged: true}

// PullRequestFile is one file the pull request changes. Lines are the
// new-side line ranges the patch adds or modifies ([start, end], 1-based,
// inclusive); LinesKnown is false when GitHub returned no patch for the file
// (binary, or a diff too large to show), so only the file is known.
type PullRequestFile struct {
	Path         string   `json:"path"`
	Status       string   `json:"status"`
	PreviousPath string   `json:"previous_path,omitempty"`
	LinesKnown   bool     `json:"lines_known"`
	Lines        [][2]int `json:"lines,omitempty"`
}

// Reviewable reports whether the file exists at the head commit (a removed
// file has nothing left to review).
func (f PullRequestFile) Reviewable() bool { return f.Status != FileRemoved }

// PullRequest is the pin of a pull-request run, fixed when the run is
// queued: the pull request, its base and head commits, its head repository
// (a fork for outside contributions) and the files it changes. The worker
// fetches HeadSHA from the base repository's refs/pull/NUMBER/head.
// Comment is whether this run posts its result as the pull request's
// CODEYE comment (docs/GITHUB.md#결과-댓글).
type PullRequest struct {
	Host           string    `json:"host"`
	Repository     string    `json:"repository"` // OWNER/REPO of the base repository
	Number         int       `json:"number"`
	URL            string    `json:"url"`
	Title          string    `json:"title"`
	State          string    `json:"state"`
	Draft          bool      `json:"draft"`
	BaseRef        string    `json:"base_ref"`
	BaseSHA        string    `json:"base_sha"`
	HeadRef        string    `json:"head_ref"`
	HeadSHA        string    `json:"head_sha"`
	HeadRepository string    `json:"head_repository"`
	ResolvedAt     time.Time `json:"resolved_at"`
	// ChangedFiles is the pull request's own changed-file count; Files can
	// be shorter when GitHub lists only part of a very large pull request
	// (FilesTruncated).
	ChangedFiles   int               `json:"changed_files"`
	FilesTruncated bool              `json:"files_truncated"`
	Files          []PullRequestFile `json:"files"`
	Comment        bool              `json:"comment"`
}

// Limits of a stored pull-request pin.
const (
	MaxPullRequestFiles  = 3000
	MaxPullRequestRanges = 2000
	maxPullRequestTitle  = 512
)

var (
	ownerRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,99}/[A-Za-z0-9._-]{1,100}$`)
	objectSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

// ValidObjectID reports a lowercase SHA-1 or SHA-256 object ID.
func ValidObjectID(s string) bool { return objectSHA.MatchString(s) }

// ValidRepositoryName reports an OWNER/REPO name.
func ValidRepositoryName(s string) bool {
	return ownerRepo.MatchString(s) && !strings.HasSuffix(s, ".git") && !strings.Contains(s, "/.")
}

// Validate checks a stored pin.
func (p PullRequest) Validate() error {
	switch {
	case ValidateHost(p.Host) != nil:
		return invalidf("pull_request.host")
	case !ValidRepositoryName(p.Repository):
		return invalidf("pull_request.repository must be OWNER/REPO")
	case p.Number < 1 || p.Number > MaxPullRequestNumber:
		return invalidf("pull_request.number")
	case p.URL != PullRequestURL(p.Host, p.Repository, p.Number):
		return invalidf("pull_request.url")
	case !utf8.ValidString(p.Title) || len(p.Title) > maxPullRequestTitle:
		return invalidf("pull_request.title")
	case p.State != "open" && p.State != "closed":
		return invalidf("pull_request.state")
	case !ValidObjectID(p.BaseSHA) || !ValidObjectID(p.HeadSHA):
		return invalidf("pull_request base_sha and head_sha must be object IDs")
	case p.HeadRepository != "" && !ValidRepositoryName(p.HeadRepository):
		return invalidf("pull_request.head_repository")
	case len(p.BaseRef) > 255 || len(p.HeadRef) > 255 || !utf8.ValidString(p.BaseRef) || !utf8.ValidString(p.HeadRef):
		return invalidf("pull_request refs")
	case p.ResolvedAt.IsZero():
		return invalidf("pull_request.resolved_at")
	case p.ChangedFiles < 0 || len(p.Files) > MaxPullRequestFiles:
		return invalidf("pull_request files")
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		if ValidateEvidencePath(f.Path) != nil || seen[f.Path] || !fileStatuses[f.Status] {
			return invalidf("pull_request file %q", f.Path)
		}
		seen[f.Path] = true
		if f.PreviousPath != "" && ValidateEvidencePath(f.PreviousPath) != nil {
			return invalidf("pull_request file %q previous_path", f.Path)
		}
		if len(f.Lines) > MaxPullRequestRanges || (!f.LinesKnown && len(f.Lines) > 0) {
			return invalidf("pull_request file %q lines", f.Path)
		}
		last := 0
		for _, r := range f.Lines {
			if r[0] < 1 || r[1] < r[0] || r[0] <= last {
				return invalidf("pull_request file %q line ranges", f.Path)
			}
			last = r[1]
		}
	}
	return nil
}

// ParsePullRequest strictly decodes and validates a stored pin.
func ParsePullRequest(raw []byte) (PullRequest, error) {
	var p PullRequest
	if err := DecodeStrict(raw, &p); err != nil {
		return PullRequest{}, err
	}
	return p, p.Validate()
}

// Marshal is the canonical stored form of the pin.
func (p PullRequest) Marshal() (json.RawMessage, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(raw)
}

// PullRequestURL is the browser address of a pull request.
func PullRequestURL(host, repository string, number int) string {
	return "https://" + strings.ToLower(host) + "/" + repository + "/pull/" + strconv.Itoa(number)
}

// SplitPullRequestURL reads a pull-request browser address
// (https://HOST/OWNER/REPO/pull/NUMBER, optionally with /files, /commits or
// /checks after it; https:// may be left out) into the repository URL and
// the number. ok is false when raw is not such an address.
func SplitPullRequestURL(raw string) (repoURL string, number int, ok bool) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") && !strings.Contains(raw, "@") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return "", 0, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || len(parts) > 5 || (parts[2] != "pull" && parts[2] != "pulls") {
		return "", 0, false
	}
	if len(parts) == 5 && parts[4] != "files" && parts[4] != "commits" && parts[4] != "checks" {
		return "", 0, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n < 1 || n > MaxPullRequestNumber || strconv.Itoa(n) != parts[3] {
		return "", 0, false
	}
	repo := "https://" + u.Host + "/" + parts[0] + "/" + parts[1]
	if _, err := RepositoryHost(repo); err != nil {
		return "", 0, false
	}
	return repo, n, true
}

// RepositoryPath is the OWNER/REPO of a validated repository URL (without a
// trailing .git).
func RepositoryPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
}

// PullRequestJob is what a node needs of a pull-request run: the number
// (the worker fetches refs/pull/NUMBER/head), the pinned commits and the
// changed paths that exist at the head commit (removed files have nothing
// to review), sorted. It travels in the claim's job only for such runs.
type PullRequestJob struct {
	Number  int      `json:"number"`
	BaseSHA string   `json:"base_sha"`
	HeadSHA string   `json:"head_sha"`
	Changed []string `json:"changed"`
}

// Job is the node view of the pin.
func (p PullRequest) Job() *PullRequestJob {
	j := &PullRequestJob{Number: p.Number, BaseSHA: p.BaseSHA, HeadSHA: p.HeadSHA, Changed: []string{}}
	for _, f := range p.Files {
		if f.Reviewable() {
			j.Changed = append(j.Changed, f.Path)
		}
	}
	sort.Strings(j.Changed)
	return j
}

// Validate checks a job's pull request.
func (j PullRequestJob) Validate() error {
	if j.Number < 1 || j.Number > MaxPullRequestNumber || !ValidObjectID(j.BaseSHA) || !ValidObjectID(j.HeadSHA) || len(j.Changed) > MaxPullRequestFiles {
		return invalidf("pull_request job")
	}
	for i, p := range j.Changed {
		if ValidateEvidencePath(p) != nil || (i > 0 && j.Changed[i-1] >= p) {
			return invalidf("pull_request job changed paths")
		}
	}
	return nil
}
