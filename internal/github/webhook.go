package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// VerifyWebhook checks GitHub's HMAC over the original request body.
func VerifyWebhook(secret string, body []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") || len(signature) != len("sha256=")+sha256.Size*2 {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), provided)
}

var webhookLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)

type webhookUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

// WebhookEvent holds only the fields needed for a repository delivery.
// Comment text is read at intake and never persisted.
type WebhookEvent struct {
	Action     string `json:"action"`
	Number     int    `json:"number"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender      webhookUser     `json:"sender"`
	PullRequest PullRequestInfo `json:"pull_request"`
	Issue       struct {
		Number      int             `json:"number"`
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		ID   int64       `json:"id"`
		Body string      `json:"body"`
		User webhookUser `json:"user"`
	} `json:"comment"`
}

// CommentCommand accepts a line consisting of the platform account mention.
// Quoted lines, Markdown code blocks and HTML blocks are excluded.
func (e WebhookEvent) CommentCommand(platformLogin string) bool {
	if e.Action != "created" || e.Issue.Number <= 0 || len(e.Issue.PullRequest) == 0 || e.Issue.PullRequest[0] != '{' ||
		e.Comment.ID <= 0 || !webhookLogin.MatchString(e.Comment.User.Login) || !webhookLogin.MatchString(platformLogin) ||
		e.Comment.User.ID <= 0 || e.Comment.User.ID != e.Sender.ID ||
		e.Comment.User.Type != "User" || e.Sender.Type != "User" || !strings.EqualFold(e.Comment.User.Login, e.Sender.Login) ||
		strings.EqualFold(e.Comment.User.Login, platformLogin) {
		return false
	}
	return containsCommand(e.Comment.Body, "@"+platformLogin)
}

func containsCommand(body, mention string) bool {
	var fence byte
	fenceSize := 0
	inlineTicks := 0
	quoted := false
	htmlEnd := ""
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		t := strings.TrimLeft(line, " ")
		if htmlEnd != "" {
			if strings.Contains(strings.ToLower(t), htmlEnd) {
				htmlEnd = ""
			}
			continue
		}
		if len(line)-len(t) >= 4 || strings.HasPrefix(t, "\t") {
			continue
		}
		if t == "" {
			quoted = false
			inlineTicks = 0
			continue
		}
		if t[0] == '`' || t[0] == '~' {
			length := len(t) - len(strings.TrimLeft(t, string(t[0])))
			if length >= 3 && fenceSize == 0 {
				fence, fenceSize = t[0], length
				continue
			} else if t[0] == fence && length >= fenceSize && strings.TrimSpace(t[length:]) == "" {
				fence, fenceSize = 0, 0
				continue
			}
		}
		if fenceSize > 0 {
			continue
		}
		lower := strings.ToLower(t)
		for _, tag := range []string{"pre", "code", "blockquote", "script", "style", "textarea"} {
			if strings.HasPrefix(lower, "<"+tag+">") || strings.HasPrefix(lower, "<"+tag+" ") {
				htmlEnd = "</" + tag + ">"
				break
			}
		}
		if strings.HasPrefix(lower, "<!--") {
			htmlEnd = "-->"
		}
		if htmlEnd != "" {
			if strings.Contains(lower, htmlEnd) {
				htmlEnd = ""
			}
			continue
		}
		if strings.HasPrefix(t, ">") || strings.HasPrefix(t, "<") {
			quoted = true
		}
		candidate := strings.TrimSpace(t)
		if !quoted && inlineTicks == 0 && strings.EqualFold(candidate, mention) && webhookLogin.MatchString(strings.TrimPrefix(candidate, "@")) {
			return true
		}
		for {
			_, tail, found := strings.Cut(t, "`")
			if !found {
				break
			}
			t = strings.TrimLeft(tail, "`")
			size := 1 + len(tail) - len(t)
			if inlineTicks == size {
				inlineTicks = 0
			} else if inlineTicks == 0 {
				inlineTicks = size
			}
		}
	}
	return false
}

// CanRequestReview checks the comment author's current repository permission.
func (c *Client) CanRequestReview(ctx context.Context, repository, login string, userID int64) (bool, error) {
	if !domain.ValidRepositoryName(repository) || !webhookLogin.MatchString(login) || userID <= 0 {
		return false, &Error{Kind: KindInvalid}
	}
	var out struct {
		Permission string `json:"permission"`
		User       User   `json:"user"`
	}
	_, err := c.do(ctx, http.MethodGet, repoPath(repository)+"/collaborators/"+url.PathEscape(login)+"/permission", nil, &out)
	if KindOf(err) == KindNotFound {
		return false, nil
	}
	return out.User.ID == userID && (out.Permission == "write" || out.Permission == "admin"), err
}
