package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ErrUnsupported marks syntactically understood input that this version does
// not support (for example SSH Git URLs). It is distinct from ErrInvalid so a
// transport can report SOURCE_UNSUPPORTED instead of a validation failure.
var ErrUnsupported = errors.New("unsupported")

func unsupportedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// SourceKind is the kind of project source declaration.
type SourceKind string

const (
	SourceGit   SourceKind = "git"
	SourceLocal SourceKind = "local"
)

// Source is a declared project source. Declaring a source never proves it is
// reachable: access is checked only when a snapshot is acquired.
type Source struct {
	Kind          SourceKind `json:"kind"`
	ProviderID    string     `json:"provider_id,omitempty"`
	RepositoryURL string     `json:"repository_url,omitempty"`
	Branch        string     `json:"branch,omitempty"`
	SourceRootID  string     `json:"source_root_id,omitempty"`
	RelativePath  string     `json:"relative_path,omitempty"`
}

// CapabilityGap names a product capability that is not available yet, so
// responses can say what has not been verified instead of implying success.
type CapabilityGap string

const (
	GapSourceUnverified     CapabilityGap = "source_access_unverified"
	GapExecutionUnavailable CapabilityGap = "execution_unavailable"
	GapReportUnavailable    CapabilityGap = "report_unavailable"
)

// DecodeStrict decodes a single JSON object into v. It rejects invalid UTF-8,
// duplicate keys, trailing data, nesting over the canonical limit and unknown
// fields. Errors wrap ErrInvalid.
func DecodeStrict(raw []byte, v any) error {
	if _, err := CanonicalJSON(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalidf("json: %v", err)
	}
	return nil
}

// ParseSource strictly decodes and validates a source declaration.
func ParseSource(raw []byte) (Source, error) {
	var s Source
	if err := DecodeStrict(raw, &s); err != nil {
		return Source{}, err
	}
	return s, s.Validate()
}

// Validate checks the declaration lexically. It does not contact providers or
// touch the filesystem.
func (s Source) Validate() error {
	switch s.Kind {
	case SourceLocal:
		if s.ProviderID != "" || s.RepositoryURL != "" || s.Branch != "" {
			return invalidf("local source must not contain git fields")
		}
		if !ValidID(s.SourceRootID) {
			return invalidf("source_root_id must be a canonical UUID")
		}
		return ValidateRelativeSourcePath(s.RelativePath)
	case SourceGit:
		if s.SourceRootID != "" || s.RelativePath != "" {
			return invalidf("git source must not contain local fields")
		}
		if !ValidID(s.ProviderID) {
			return invalidf("provider_id must be a canonical UUID")
		}
		if _, err := RepositoryHost(s.RepositoryURL); err != nil {
			return err
		}
		if s.Branch == "" {
			// The repository's default branch, resolved when the snapshot is taken.
			return nil
		}
		return ValidateBranchName(s.Branch)
	default:
		return invalidf("source kind must be git or local")
	}
}

// ValidateRelativeSourcePath accepts "." (the source root itself) or a
// slash-separated relative path under the root.
func ValidateRelativeSourcePath(p string) error {
	if p == "." {
		return nil
	}
	if err := ValidateEvidencePath(p); err != nil {
		return fmt.Errorf("relative_path: %w", err)
	}
	return nil
}

var repoPath = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`)

// RepositoryHost validates a public HTTPS repository URL of the form
// https://host[:port]/owner/repo[.git] and returns its lowercase host[:port].
// URLs with credentials, query or fragment are invalid; SSH, scp-like and
// git:// URLs are unsupported.
func RepositoryHost(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 || !utf8.ValidString(raw) {
		return "", invalidf("repository_url is empty, too long or not UTF-8")
	}
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "ssh://"), strings.HasPrefix(lower, "git://"), strings.HasPrefix(lower, "git+ssh://"):
		return "", unsupportedf("repository_url scheme is not supported; use https")
	case !strings.Contains(raw, "://") && strings.Contains(raw, ":") && strings.Contains(raw, "@"):
		return "", unsupportedf("scp-like repository_url is not supported; use https")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", invalidf("repository_url is not a URL")
	}
	switch {
	case u.Scheme != "https":
		return "", invalidf("repository_url must use https")
	case u.User != nil:
		return "", invalidf("repository_url must not contain credentials")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#"):
		return "", invalidf("repository_url must not contain a query or fragment")
	case u.Host == "" || u.Opaque != "":
		return "", invalidf("repository_url has no host")
	case u.RawPath != "" || strings.Contains(u.Path, ".."):
		return "", invalidf("repository_url path must be /owner/repo")
	}
	p := strings.TrimSuffix(u.Path, "/")
	if !repoPath.MatchString(p) || strings.HasSuffix(p, "/.git") {
		return "", invalidf("repository_url path must be /owner/repo")
	}
	if err := ValidateHost(u.Host); err != nil {
		return "", err
	}
	return strings.ToLower(u.Host), nil
}

var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*(:[0-9]{1,5})?$`)

// ValidateHost accepts a lowercase-insensitive DNS host name with an optional
// port. IP literals and userinfo are rejected.
func ValidateHost(h string) error {
	if len(h) > 253 || !hostPattern.MatchString(strings.ToLower(h)) {
		return invalidf("host must be a DNS name with optional port")
	}
	name := h
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		name = h[:i]
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return invalidf("host must be a DNS name, not an IP literal")
	}
	return nil
}

// SplitRepositoryURL turns what a user pastes into a repository URL and a
// branch: "host/owner/repo" gets https://, a trailing slash is dropped and
// a browser address of a branch (https://HOST/OWNER/REPO/tree/BRANCH) is
// split into the repository and the branch ("" when the address names none).
// The result is validated like a declared source.
func SplitRepositoryURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") && !strings.Contains(raw, "@") && strings.Count(raw, "/") >= 2 {
		raw = "https://" + raw
	}
	branch := ""
	if u, err := url.Parse(raw); err == nil && u.Scheme == "https" && u.RawQuery == "" && u.Fragment == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 3 && parts[2] == "tree" {
			branch = strings.Join(parts[3:], "/")
			parts = parts[:2]
		}
		if len(parts) == 2 {
			u.Path = "/" + strings.Join(parts, "/")
			raw = u.String()
		}
	}
	if _, err := RepositoryHost(raw); err != nil {
		return "", "", err
	}
	if branch != "" {
		if err := ValidateBranchName(branch); err != nil {
			return "", "", err
		}
	}
	return raw, branch, nil
}

// SameSource reports whether a and b declare the same source: the same
// root and relative path, or the same provider, branch and repository (the
// URL compared without case, a trailing slash or .git).
func SameSource(a, b Source) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == SourceLocal {
		return a.SourceRootID == b.SourceRootID && a.RelativePath == b.RelativePath
	}
	repo := func(u string) string { return strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")) }
	return a.ProviderID == b.ProviderID && a.Branch == b.Branch && repo(a.RepositoryURL) == repo(b.RepositoryURL)
}

// RepositoryName returns the last path segment of a validated repository URL
// without a trailing .git.
func RepositoryName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := strings.TrimSuffix(u.Path, "/")
	return strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".git")
}

// ValidateBranchName applies a conservative subset of git check-ref-format.
func ValidateBranchName(b string) error {
	switch {
	case b == "" || len(b) > 255 || !utf8.ValidString(b):
		return invalidf("branch is empty, too long or not UTF-8")
	case b == "@" || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, "."):
		return invalidf("branch has an invalid start or end")
	case strings.Contains(b, "..") || strings.Contains(b, "//") || strings.Contains(b, "@{"):
		return invalidf("branch contains a forbidden sequence")
	case strings.HasPrefix(b, "-"):
		return invalidf("branch must not start with '-'")
	}
	for _, r := range b {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return invalidf("branch contains a forbidden character")
		}
	}
	for _, c := range strings.Split(b, "/") {
		if strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".lock") {
			return invalidf("branch component has a forbidden form")
		}
	}
	return nil
}

// RunRequest is the client body of a run creation request. The server, not the
// client, resolves commands and credentials from the stored project revision.
type RunRequest struct {
	Profile      string `json:"profile"`
	SourcePolicy string `json:"source_policy"`
	Scope        Scope  `json:"scope"`
}

// Scope selects what part of the source a run covers.
type Scope struct {
	Kind  string   `json:"kind"`
	Paths []string `json:"paths,omitempty"`
}

const maxScopePaths = 100

// ParseRunRequest strictly decodes and validates a run creation body.
func ParseRunRequest(raw []byte) (RunRequest, error) {
	var r RunRequest
	if err := DecodeStrict(raw, &r); err != nil {
		return RunRequest{}, err
	}
	return r, r.Validate()
}

// Validate checks a decoded run request.
func (r RunRequest) Validate() error {
	switch r.Profile {
	case "deep-review", "with-tests", "focused":
	default:
		return invalidf("profile must be deep-review, with-tests or focused")
	}
	if r.SourcePolicy != "latest_at_acquire" {
		return invalidf("source_policy must be latest_at_acquire")
	}
	switch r.Scope.Kind {
	case "full":
		if r.Scope.Paths != nil {
			return invalidf("scope paths are only allowed with kind paths")
		}
	case "paths":
		if len(r.Scope.Paths) == 0 || len(r.Scope.Paths) > maxScopePaths {
			return invalidf("scope paths must contain 1..%d entries", maxScopePaths)
		}
		seen := map[string]bool{}
		for _, p := range r.Scope.Paths {
			if err := ValidateEvidencePath(p); err != nil {
				return fmt.Errorf("scope path: %w", err)
			}
			if seen[p] {
				return invalidf("scope paths must be unique")
			}
			seen[p] = true
		}
	default:
		return invalidf("scope kind must be full or paths")
	}
	return nil
}
