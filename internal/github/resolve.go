package github

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Resolve pins a pull request of host/repository for a run: its base and
// head commits, its head repository and the changed files with the new-side
// line ranges of their patches. A file without a patch keeps LinesKnown
// false (the report then falls back to the file); a listing shorter than the
// pull request's own count is marked FilesTruncated. Paths that are not
// portable snapshot paths are left out and also mark the listing truncated.
func Resolve(ctx context.Context, c *Client, host, repository string, number int, now time.Time) (domain.PullRequest, error) {
	info, err := c.PullRequest(ctx, repository, number)
	if err != nil {
		return domain.PullRequest{}, err
	}
	return resolveInfo(ctx, c, host, repository, number, now, info)
}

// ResolveWebhook checks that both sides still belong to the configured
// repository. An old event cannot request a newer head, and an external fork
// cannot enter native analysis through the webhook.
func ResolveWebhook(ctx context.Context, c *Client, host, repository string, id int64, number int, head string, now time.Time) (domain.PullRequest, bool, error) {
	info, err := c.PullRequest(ctx, repository, number)
	if err != nil {
		return domain.PullRequest{}, false, err
	}
	if !sameRepositoryPullRequest(info, id, repository) || (head != "" && info.Head.SHA != head) {
		return domain.PullRequest{}, false, nil
	}
	pin, err := resolveInfo(ctx, c, host, repository, number, now, info)
	if err != nil {
		return domain.PullRequest{}, false, err
	}
	latest, err := c.PullRequest(ctx, repository, number)
	if err != nil {
		return domain.PullRequest{}, false, err
	}
	// The files endpoint follows the live pull request. Check that the
	// commits used to pin the file range did not move while listing it.
	unchanged := latest.Number == number && latest.Base.SHA == pin.BaseSHA && latest.Head.SHA == pin.HeadSHA
	return pin, unchanged && sameRepositoryPullRequest(latest, id, repository), nil
}

func sameRepositoryPullRequest(info PullRequestInfo, id int64, repository string) bool {
	return info.Base.Repo != nil && info.Head.Repo != nil && info.Base.Repo.ID == id && info.Head.Repo.ID == id &&
		strings.EqualFold(info.Base.Repo.FullName, repository) && strings.EqualFold(info.Head.Repo.FullName, repository) &&
		info.State == "open" && !info.Draft
}

func resolveInfo(ctx context.Context, c *Client, host, repository string, number int, now time.Time, info PullRequestInfo) (domain.PullRequest, error) {
	if info.Number != number || !domain.ValidObjectID(info.Base.SHA) || !domain.ValidObjectID(info.Head.SHA) {
		return domain.PullRequest{}, &Error{Kind: KindInvalid}
	}
	files, err := c.PullRequestFiles(ctx, repository, number)
	if err != nil {
		return domain.PullRequest{}, err
	}
	p := domain.PullRequest{Host: strings.ToLower(host), Repository: repository, Number: number, URL: domain.PullRequestURL(host, repository, number),
		Title: clip(info.Title, 512), State: info.State, Draft: info.Draft, BaseRef: clip(info.Base.Ref, 255), BaseSHA: info.Base.SHA,
		HeadRef: clip(info.Head.Ref, 255), HeadSHA: info.Head.SHA, ResolvedAt: now.UTC().Truncate(time.Second), ChangedFiles: info.ChangedFiles, Files: []domain.PullRequestFile{}}
	if p.State != "closed" {
		p.State = "open"
	}
	if info.Head.Repo != nil && domain.ValidRepositoryName(info.Head.Repo.FullName) {
		p.HeadRepository = info.Head.Repo.FullName
	}
	seen := map[string]bool{}
	for _, f := range files {
		if len(p.Files) >= domain.MaxPullRequestFiles {
			p.FilesTruncated = true
			break
		}
		if domain.ValidateEvidencePath(f.Filename) != nil || seen[f.Filename] {
			p.FilesTruncated = true
			continue
		}
		seen[f.Filename] = true
		pf := domain.PullRequestFile{Path: f.Filename, Status: f.Status}
		switch f.Status {
		case domain.FileAdded, domain.FileModified, domain.FileRemoved, domain.FileRenamed, domain.FileCopied, domain.FileChanged, domain.FileUnchanged:
		default:
			pf.Status = domain.FileChanged
		}
		if f.PreviousFilename != "" && domain.ValidateEvidencePath(f.PreviousFilename) == nil {
			pf.PreviousPath = f.PreviousFilename
		}
		if f.Patch != nil && pf.Status != domain.FileRemoved {
			if lines, ok := ChangedLines(*f.Patch); ok {
				pf.LinesKnown, pf.Lines = true, lines
			}
		}
		p.Files = append(p.Files, pf)
	}
	if len(files) < info.ChangedFiles {
		p.FilesTruncated = true
	}
	if err := p.Validate(); err != nil {
		return domain.PullRequest{}, &Error{Kind: KindInvalid}
	}
	return p, nil
}

func clip(s string, n int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ChangedLines reads a unified-diff patch (GitHub's per-file patch, hunks
// only) and returns the new-side ranges of added lines, merged where they
// touch. ok is false when the patch does not parse or has more ranges than
// a pin keeps; the caller then knows only the file.
func ChangedLines(patch string) ([][2]int, bool) {
	var out [][2]int
	line, inHunk := 0, false
	add := func(n int) {
		if k := len(out); k > 0 && out[k-1][1]+1 >= n {
			out[k-1][1] = max(out[k-1][1], n)
			return
		}
		out = append(out, [2]int{n, n})
	}
	for _, l := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			start, ok := hunkStart(l)
			if !ok {
				return nil, false
			}
			line, inHunk = start, true
		case !inHunk:
			if strings.TrimSpace(l) != "" {
				return nil, false
			}
		case strings.HasPrefix(l, "+"):
			add(line)
			line++
		case strings.HasPrefix(l, "-"):
		case strings.HasPrefix(l, `\`):
			// "\ No newline at end of file"
		default:
			line++
		}
		if len(out) > domain.MaxPullRequestRanges {
			return nil, false
		}
	}
	if out == nil {
		out = [][2]int{}
	}
	return out, true
}

// hunkStart is the new-side start line of a hunk header
// "@@ -a,b +c,d @@ ...". An empty new side (+0,0) starts at 1 for counting.
func hunkStart(h string) (int, bool) {
	_, rest, ok := strings.Cut(h, " +")
	if !ok {
		return 0, false
	}
	spec, _, ok := strings.Cut(rest, " ")
	if !ok {
		return 0, false
	}
	first, _, _ := strings.Cut(spec, ",")
	n, err := strconv.Atoi(first)
	if err != nil || n < 0 {
		return 0, false
	}
	return max(n, 1), true
}
