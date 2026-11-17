package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review-v1"
)

const MediaType = "application/vnd.codeye.review+json"

type Caller interface {
	Review(context.Context, executor.Runner, opencode.Request) opencode.Reply
}
type Handler struct {
	Static analysis.Handler
	Caller Caller
}
type Fact struct {
	Kind      string   `json:"kind"`
	Statement string   `json:"statement"`
	Evidence  []string `json:"evidence_ids"`
}
type Candidate struct {
	ID           string   `json:"id,omitempty"`
	Title        string   `json:"title"`
	Condition    string   `json:"condition"`
	Impact       string   `json:"impact"`
	Improvement  string   `json:"improvement"`
	Counterpoint string   `json:"counterpoint"`
	Evidence     []string `json:"evidence_ids"`
}
type Provenance struct {
	Role          string          `json:"role"`
	Model         string          `json:"model"`
	Version       string          `json:"opencode_version"`
	PackDigest    string          `json:"pack_sha256"`
	PackFiles     []string        `json:"pack_files"`
	RequestDigest string          `json:"request_sha256"`
	Usage         *opencode.Usage `json:"usage"`
}
type Record struct {
	Schema      string       `json:"schema_version"`
	Snapshot    string       `json:"snapshot_manifest_digest"`
	RunID       string       `json:"run_id"`
	AttemptID   string       `json:"attempt_id"`
	Stage       string       `json:"stage"`
	Evidence    []Excerpt    `json:"evidence"`
	Facts       []Fact       `json:"facts"`
	Candidates  []Candidate  `json:"candidates"`
	Limitations []string     `json:"limitations"`
	Calls       []Provenance `json:"calls"`
}

func (h Handler) Info() analysis.HandlerInfo { return h.Static.Info() }
func (h Handler) Plan(ctx context.Context, j analysis.Job) ([]analysis.TaskSpec, error) {
	plan, err := h.Static.Plan(ctx, j)
	if err != nil {
		return nil, err
	}
	return append(plan, analysis.TaskSpec{Key: "review.context", Required: true, Title: "프로젝트 맥락 검토"},
		analysis.TaskSpec{Key: "review.investigate", Required: true, Title: "코드 근거와 반례 검토"}), nil
}
func (h Handler) Run(ctx context.Context, j analysis.Job, t analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	role, review := strings.CutPrefix(t.Key, "review.")
	if !review {
		return h.Static.Run(ctx, j, t, runner)
	}
	if !j.AllowModelReview {
		return result(domain.TaskSkippedPolicy, "model_source_not_approved")
	}
	if h.Caller == nil {
		return result(domain.TaskUnsupported, "model_not_configured")
	}
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		return result(domain.TaskFailed, "review_input_unavailable")
	}
	record := Record{Schema: "0.1", Snapshot: c.Snapshot, RunID: j.RunID, AttemptID: j.AttemptID, Stage: role,
		Evidence: append([]Excerpt(nil), c.Excerpts...), Facts: []Fact{}, Candidates: []Candidate{}, Limitations: c.Limitations, Calls: []Provenance{}}
	for i := range record.Evidence {
		record.Evidence[i].Text = ""
	}
	input := struct {
		Corpus Corpus `json:"scope"`
		Facts  []Fact `json:"facts,omitempty"`
	}{Corpus: c}
	if role == "investigate" {
		record, err = readRecord(j, "context")
		if err != nil {
			return result(domain.TaskSkippedPolicy, "dependency_failed")
		}
		input.Facts = record.Facts
		record.Stage = role
	}
	query, err := json.Marshal(input)
	if err != nil {
		return result(domain.TaskFailed, "review_input_unavailable")
	}
	pack, err := reviewpack.For(role)
	if err != nil {
		return result(domain.TaskFailed, "review_pack_invalid")
	}
	contract := contracts[role]
	reply := h.Caller.Review(ctx, runner, opencode.Request{Role: role, Scratch: j.Scratch, SnapshotDigest: c.Snapshot,
		Instructions: pack.Text + "\n" + contract + "\nUse only the provided excerpts. Review ordinary software behavior and maintainability. No code execution, test generation or external access. Cite only evidence IDs given in scope. Write concise Korean prose.",
		Input:        query, ProtectedRoots: []string{j.Snapshot.SnapshotDir}})
	if reply.Reason != "" {
		return failure(reply)
	}
	if err = accept(role, reply.Output, &record); err != nil {
		return result(domain.TaskFailed, "review_evidence_invalid")
	}
	record.Calls = append(record.Calls, Provenance{Role: role, Model: reply.Model, Version: reply.Version, PackDigest: pack.Digest, PackFiles: pack.Files, RequestDigest: reply.RequestDigest, Usage: reply.Usage})
	return writeRecord(j, record)
}

var contracts = map[string]string{
	"context":     `For this role use exactly this result contract, ignoring any wider output examples: {"facts":[{"kind":"observed|documented|inferred","statement":"one grounded fact","evidence_ids":["ev_..."]}]}. Return 1-4 facts, including limitations in the statements when needed. Use documented only for document/config references.`,
	"investigate": `For this role use exactly {"candidates":[{"title":"...","condition":"concrete trigger","impact":"...","improvement":"...","counterpoint":"actual counterevidence or remaining uncertainty","evidence_ids":["ev_..."]}]}. Return zero to four independent quality candidates. Include a code or test evidence ID for each. Do not claim reproduction. Do not produce exploit or attack instructions.`,
}

func result(status domain.TaskStatus, reason string) analysis.TaskResult {
	return analysis.TaskResult{Status: status, Reason: reason}
}
func failure(reply opencode.Reply) analysis.TaskResult {
	switch reply.Reason {
	case "cancelled":
		return result(domain.TaskCancelled, reply.Reason)
	case "model_timeout":
		return result(domain.TaskTimedOut, reply.Reason)
	case "model_not_configured", "opencode_version_unsupported":
		return result(domain.TaskUnsupported, reply.Reason)
	case "policy_blocked":
		return result(domain.TaskSkippedPolicy, reply.Reason)
	}
	return result(domain.TaskFailed, reply.Reason)
}
func safeText(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 2400 && !sensitiveText.MatchString(s)
}
func refsOK(ids []string, evidence []Excerpt, kind string) bool {
	if len(ids) == 0 || len(ids) > 6 {
		return false
	}
	seen := map[string]bool{}
	hasKind := kind == ""
	for _, id := range ids {
		if seen[id] {
			return false
		}
		seen[id] = true
		found := false
		for _, e := range evidence {
			if e.ID != id {
				continue
			}
			found = true
			if e.Kind == kind || (kind == "code" && e.Kind == "test") || (kind == "document" && e.Kind == "config") {
				hasKind = true
			}
			break
		}
		if !found {
			return false
		}
	}
	return hasKind
}

func accept(role string, raw json.RawMessage, r *Record) error {
	bad := errors.New("invalid model evidence")
	switch role {
	case "context":
		var res struct {
			Facts []Fact `json:"facts"`
		}
		if domain.DecodeStrict(raw, &res) != nil || len(res.Facts) == 0 || len(res.Facts) > 4 {
			return bad
		}
		for _, f := range res.Facts {
			kind := ""
			switch f.Kind {
			case "documented":
				kind = "document"
			case "observed", "inferred":
			default:
				return bad
			}
			if !safeText(f.Statement) || !refsOK(f.Evidence, r.Evidence, kind) {
				return bad
			}
		}
		r.Facts = res.Facts
	case "investigate":
		var res struct {
			Candidates []Candidate `json:"candidates"`
		}
		if domain.DecodeStrict(raw, &res) != nil || res.Candidates == nil || len(res.Candidates) > 4 {
			return bad
		}
		for i := range res.Candidates {
			c := &res.Candidates[i]
			if c.ID != "" || !safeText(c.Title) || !safeText(c.Condition) || !safeText(c.Impact) || !safeText(c.Improvement) || !safeText(c.Counterpoint) || !refsOK(c.Evidence, r.Evidence, "code") {
				return bad
			}
			c.ID = fmt.Sprintf("candidate_%d", i+1)
		}
		r.Candidates = res.Candidates
	default:
		return bad
	}
	return nil
}

func readRecord(j analysis.Job, role string) (Record, error) {
	var rec Record
	b, err := os.ReadFile(filepath.Join(j.Scratch, "review."+role+".json"))
	if err != nil {
		return rec, err
	}
	if len(b) > 256<<10 || domain.DecodeStrict(b, &rec) != nil || rec.Stage != role || rec.Schema != "0.1" || rec.Snapshot != j.Snapshot.Digest || rec.RunID != j.RunID || rec.AttemptID != j.AttemptID {
		return Record{}, errors.New("invalid prior review")
	}
	return rec, nil
}
func writeRecord(j analysis.Job, r Record) analysis.TaskResult {
	b, err := json.Marshal(r)
	p := filepath.Join(j.Scratch, "review."+r.Stage+".json")
	if err != nil || len(b) > 256<<10 || os.WriteFile(p, bytes.TrimSpace(b), 0600) != nil {
		return result(domain.TaskFailed, "review_record_failed")
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{{Path: p, Role: "other", MediaType: MediaType}}}
}
