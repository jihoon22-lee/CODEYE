package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review-v1"
)

const MediaType = record.MediaType

type Caller interface {
	Review(context.Context, executor.Runner, opencode.Request) opencode.Reply
}
type Handler struct {
	Static analysis.Handler
	Caller Caller
}

// Record types live in the I/O-free record package so report decoders can
// validate artifacts without importing model or process code.
type (
	Fact         = record.Fact
	Candidate    = record.Candidate
	Provenance   = record.Call
	Record       = record.Record
	Excerpt      = record.Excerpt
	Verification = record.Verification
)

func (h Handler) Info() analysis.HandlerInfo { return h.Static.Info() }
func (h Handler) Plan(ctx context.Context, j analysis.Job) ([]analysis.TaskSpec, error) {
	plan, err := h.Static.Plan(ctx, j)
	if err != nil {
		return nil, err
	}
	// The legacy pack's context role reads documents and code; it is shown
	// with the documents stage. Investigation and verification are code review.
	return append(plan, analysis.TaskSpec{Key: "review.context", Required: true, Title: "프로젝트 맥락 검토", Stage: domain.StageDocuments},
		analysis.TaskSpec{Key: "review.investigate", Required: true, Title: "코드 근거와 반례 검토", Stage: domain.StageCodeReview},
		analysis.TaskSpec{Key: "review.verify", Required: true, Title: "후보 반례 검증", Stage: domain.StageCodeReview}), nil
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
	screen := screenSecrets(ctx, h.Static, j)
	if role == "verify" {
		return h.verify(ctx, j, runner, screen)
	}
	c, err := buildScreenedCorpus(j.Snapshot, j.Scope, screen.withheld)
	if err != nil {
		return result(domain.TaskFailed, "review_input_unavailable")
	}
	rec := Record{Schema: "0.1", Snapshot: c.Snapshot, RunID: j.RunID, AttemptID: j.AttemptID, Stage: role,
		Evidence: append([]Excerpt(nil), c.Excerpts...), Facts: []Fact{}, Candidates: []Candidate{}, Limitations: c.Limitations, Calls: []Provenance{}}
	for i := range rec.Evidence {
		rec.Evidence[i].Text = ""
	}
	input := struct {
		Corpus Corpus `json:"scope"`
		Facts  []Fact `json:"facts,omitempty"`
	}{Corpus: c}
	if role == "investigate" {
		rec, err = readRecord(j, "context")
		if err != nil || !sameEvidence(rec.Evidence, c.Excerpts) {
			return result(domain.TaskSkippedPolicy, "dependency_failed")
		}
		input.Facts = rec.Facts
		rec.Stage = role
	}
	// The record (never the pinned model input) says when nothing screened
	// the corpus for secrets.
	if screen.state == screenUnscreened {
		rec.Limitations = addLim(rec.Limitations, limNotSecretScreened)
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
	rec.Limitations = noteVersion(rec.Limitations, reply)
	call := Provenance{Role: role, Model: reply.Model, Version: reply.Version, PackDigest: pack.Digest, PackFiles: pack.Files, RequestDigest: reply.RequestDigest, Usage: usage(reply.Usage), Attempt: 1, Outcome: record.OutcomeAccepted}
	res := result(domain.TaskSucceeded, "")
	if reply.Reason != "" {
		res = failure(reply)
	} else if err = accept(role, reply.Output, &rec); err != nil {
		res = result(domain.TaskFailed, "review_evidence_invalid")
	}
	if res.Status != domain.TaskSucceeded {
		call.Outcome = res.Reason
		rec.Calls = append(rec.Calls, call)
		return failedRecord(j, rec, res)
	}
	rec.Calls = append(rec.Calls, call)
	rec.Outcome = record.OutcomeAccepted
	return writeRecord(j, rec)
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
	case "model_not_configured", "opencode_version_unsupported", opencode.ReasonCredentialUnavailable:
		return result(domain.TaskUnsupported, reply.Reason)
	case "policy_blocked":
		return result(domain.TaskSkippedPolicy, reply.Reason)
	}
	return result(domain.TaskFailed, reply.Reason)
}
func safeText(s string) bool { return record.SafeText(s) }

// noteVersion adds opencode_version_untested when reply came through an
// OpenCode release other than the tested one.
func noteVersion(lims []string, reply opencode.Reply) []string {
	if opencode.VersionUntested(reply.Version) {
		return addLim(lims, opencode.LimVersionUntested)
	}
	return lims
}
func accept(role string, raw json.RawMessage, r *Record) error {
	bad := errors.New("invalid model evidence")
	catalog := map[string]Excerpt{}
	for _, e := range r.Evidence {
		catalog[e.ID] = e
	}
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
			if !safeText(f.Statement) || !record.RefsOK(f.Evidence, catalog, kind, 6) {
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
			if c.ID != "" || !safeText(c.Title) || !safeText(c.Condition) || !safeText(c.Impact) || !safeText(c.Improvement) || !safeText(c.Counterpoint) || !record.RefsOK(c.Evidence, catalog, "code", 6) {
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

// expect binds record validation to this attempt and its immutable snapshot
// manifest; prior stage records in scratch are not trusted on their own.
func expect(j analysis.Job, stage string) record.Expect {
	index := map[string]string{}
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.SHA256 != "" && e.ExcludedReason == "" && inScope(e.Path, j.Scope) {
			index[e.Path] = e.SHA256
		}
	}
	return record.Expect{Snapshot: j.Snapshot.Digest, RunID: j.RunID, AttemptID: j.AttemptID, Stage: stage,
		Lookup: func(p string) (string, bool) { sha, ok := index[p]; return sha, ok }}
}

func readRecord(j analysis.Job, role string) (Record, error) {
	f, err := os.Open(filepath.Join(j.Scratch, "review."+role+".json"))
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, record.MaxBytes+1))
	if err != nil {
		return Record{}, err
	}
	rec, err := record.Decode(b, expect(j, role))
	if err != nil || !rec.Accepted() {
		return Record{}, errors.New("invalid prior review")
	}
	return rec, nil
}

func persist(j analysis.Job, r Record) (analysis.ArtifactFile, error) {
	b, err := record.MarshalRecord(r, expect(j, r.Stage))
	if err != nil {
		return analysis.ArtifactFile{}, err
	}
	p := filepath.Join(j.Scratch, "review."+r.Stage+".json")
	if err = os.WriteFile(p, bytes.TrimSpace(b), 0600); err != nil {
		return analysis.ArtifactFile{}, err
	}
	return analysis.ArtifactFile{Path: p, Role: "other", MediaType: MediaType}, nil
}

func writeRecord(j analysis.Job, r Record) analysis.TaskResult {
	art, err := persist(j, r)
	if err != nil {
		return result(domain.TaskFailed, "review_record_failed")
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}}
}

func usage(u *opencode.Usage) *record.Usage {
	if u == nil {
		return nil
	}
	return &record.Usage{Input: u.Input, Output: u.Output}
}
