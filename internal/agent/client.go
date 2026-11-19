package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// APIError is a non-2xx controller response (envelope code).
type APIError struct {
	Status    int
	Code      string
	Retryable bool
}

func (e *APIError) Error() string { return fmt.Sprintf("controller %d %s", e.Status, e.Code) }

// IsStale reports a 409 STALE_ATTEMPT.
func IsStale(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == nodeproto.CodeStaleAttempt
}

// IsUnauthenticated reports a revoked/unknown node.
func IsUnauthenticated(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

// permanent: retrying the same request cannot succeed.
func permanent(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Status != http.StatusTooManyRequests && ae.Status != http.StatusRequestTimeout
}

// Client is the node HTTP client. It never sends cookies or an Origin header
// and is the only holder of the node secret (never passed to workers).
type Client struct {
	base   string
	http   *http.Client
	nodeID string
	secret string
}

// ValidateControllerURL accepts https, or http only to a loopback host.
func ValidateControllerURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid controller URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("plain http is allowed only to a loopback controller")
		}
	default:
		return nil, errors.New("controller URL must be http(s)")
	}
	return u, nil
}

// NewClient builds a client for base (e.g. http://127.0.0.1:8080).
func NewClient(base string) (*Client, error) {
	u, err := ValidateControllerURL(base)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil // never route node credentials through an implicit proxy
	return &Client{base: strings.TrimRight(u.String(), "/") + "/api/v1", http: &http.Client{Transport: tr, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// SetCredentials installs node identity after enrollment or load.
func (c *Client) SetCredentials(nodeID, secret string) { c.nodeID, c.secret = nodeID, secret }

func (c *Client) do(ctx context.Context, method, path string, headers map[string]string, body io.Reader, contentLength int64, out any, okStatus ...int) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, err
	}
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	for _, s := range okStatus {
		if resp.StatusCode == s {
			if out != nil && len(data) > 0 {
				if err := json.Unmarshal(data, out); err != nil {
					return resp.StatusCode, fmt.Errorf("decode controller response: %w", err)
				}
			}
			return resp.StatusCode, nil
		}
	}
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &envelope)
	return resp.StatusCode, &APIError{Status: resp.StatusCode, Code: envelope.Error.Code, Retryable: envelope.Error.Retryable}
}

func (c *Client) postJSON(ctx context.Context, path string, headers map[string]string, in, out any, ok ...int) (int, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return 0, err
	}
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	return c.do(ctx, http.MethodPost, path, h, bytes.NewReader(b), int64(len(b)), out, ok...)
}

// Enroll exchanges a one-time token for node identity.
func (c *Client) Enroll(ctx context.Context, req nodeproto.EnrollRequest) (nodeproto.EnrollResponse, error) {
	var out nodeproto.EnrollResponse
	_, err := c.postJSON(ctx, "/nodes/enroll", nil, req, &out, http.StatusCreated)
	return out, err
}

// Heartbeat posts the node heartbeat.
func (c *Client) Heartbeat(ctx context.Context, req nodeproto.NodeHeartbeatRequest) (nodeproto.NodeHeartbeatResponse, error) {
	var out nodeproto.NodeHeartbeatResponse
	_, err := c.postJSON(ctx, "/nodes/"+c.nodeID+"/heartbeat", nil, req, &out, http.StatusOK)
	return out, err
}

// Claim asks for work for workerID; nil means nothing claimable (204).
func (c *Client) Claim(ctx context.Context, workerID, key string) (*nodeproto.ClaimResponse, error) {
	var out nodeproto.ClaimResponse
	status, err := c.postJSON(ctx, "/nodes/"+c.nodeID+"/claims", map[string]string{nodeproto.HeaderIdempotencyKey: key}, nodeproto.ClaimRequest{WorkerID: workerID}, &out, http.StatusOK, http.StatusNoContent)
	if err != nil || status == http.StatusNoContent {
		return nil, err
	}
	return &out, nil
}

// AttemptHeartbeat renews the lease.
func (c *Client) AttemptHeartbeat(ctx context.Context, attemptID string, req nodeproto.AttemptHeartbeatRequest) (nodeproto.AttemptHeartbeatResponse, error) {
	var out nodeproto.AttemptHeartbeatResponse
	_, err := c.postJSON(ctx, "/attempts/"+attemptID+"/heartbeat", nil, req, &out, http.StatusOK)
	return out, err
}

// Event posts one attempt event (idempotent by client_event_id).
func (c *Client) Event(ctx context.Context, attemptID string, req nodeproto.EventRequest) (json.RawMessage, error) {
	var out json.RawMessage
	_, err := c.postJSON(ctx, "/attempts/"+attemptID+"/events", nil, req, &out, http.StatusOK)
	return out, err
}

// PutArtifact uploads bytes (idempotent by client artifact id + sha).
func (c *Client) PutArtifact(ctx context.Context, attemptID, clientID, workerID string, epoch int64, role, media, sha string, body []byte) (nodeproto.ArtifactResponse, error) {
	var out nodeproto.ArtifactResponse
	h := map[string]string{
		"Content-Type":                media,
		nodeproto.HeaderWorkerID:      workerID,
		nodeproto.HeaderLeaseEpoch:    strconv.FormatInt(epoch, 10),
		nodeproto.HeaderArtifactRole:  role,
		nodeproto.HeaderContentSHA256: sha,
	}
	_, err := c.do(ctx, http.MethodPut, "/attempts/"+attemptID+"/artifacts/"+clientID, h, bytes.NewReader(body), int64(len(body)), &out, http.StatusCreated)
	return out, err
}

// Complete ends the attempt.
func (c *Client) Complete(ctx context.Context, attemptID string, req nodeproto.CompleteRequest) (nodeproto.RunAck, error) {
	var out nodeproto.RunAck
	_, err := c.postJSON(ctx, "/attempts/"+attemptID+"/complete", nil, req, &out, http.StatusAccepted)
	return out, err
}

// Cleanup confirms cleanup of an attempt.
func (c *Client) Cleanup(ctx context.Context, attemptID string, req nodeproto.CleanupRequest) (nodeproto.RunAck, error) {
	var out nodeproto.RunAck
	_, err := c.postJSON(ctx, "/attempts/"+attemptID+"/cleanup", nil, req, &out, http.StatusAccepted)
	return out, err
}

// retry runs fn with backoff until success, a permanent error, or ctx end.
// Only idempotent requests are retried.
func retry(ctx context.Context, attempts int, fn func() error) error {
	delay := 200 * time.Millisecond
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil || permanent(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
	return err
}
