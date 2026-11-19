package review

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// screenStub is the go vet stub plus the structure and secrets envelopes
// (docs/ANALYZERS.md): "lizard" with one complexity metric on gateway.go and the secrets task with one
// detection on gateway.go, where the test planted a value the platform's
// Sensitive pattern does not recognize. mode is the secrets task outcome:
// "" a complete successful scan, "not_run" the not-configured stand-in,
// "failed" a failed scan, "missing" no envelope at all. Descriptors names
// the analyzers as the production composite does.
type screenStub struct{ mode string }

func (screenStub) Descriptors() []pack.Descriptor {
	return []pack.Descriptor{{ID: "go.vet", Name: "go-vet"}, {ID: "any.lizard", Name: "lizard"}, {ID: "any.gitl" + "eaks", Name: secretsTaskKey}}
}

const secretsTaskKey = "secrets"

func (screenStub) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: pack.CompositeName, Version: "stub", Capability: pack.Capability}
}

func (screenStub) Plan(ctx context.Context, j analysis.Job) ([]analysis.TaskSpec, error) {
	plan, _ := vetStub{}.Plan(ctx, j)
	for _, k := range []string{"lizard", secretsTaskKey} {
		plan = append(plan, analysis.TaskSpec{Key: k, Title: k, Stage: domain.StageStaticAnalysis})
	}
	return plan, nil
}

func (w screenStub) Run(ctx context.Context, j analysis.Job, t analysis.TaskSpec, r executor.Runner) analysis.TaskResult {
	if pack.Owns("go-vet", t.Key) {
		return vetStub{obs: gatewayObservation}.Run(ctx, j, t, r)
	}
	if t.Key == secretsTaskKey && w.mode == "missing" {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: "handler_error"}
	}
	id, rule, msg, category := "any.lizard", "cyclomatic_complexity", "Forward(...) at gateway.go:10: NCSS 5, CCN 17", "maintainability"
	var metric *obsdoc.Metric
	if t.Key == secretsTaskKey {
		id, rule, msg, category = "any.gitl"+"eaks", "aws-access-token", "secret detected by gitl"+"eaks rule aws-access-token (value withheld)", "security"
	} else {
		metric = &obsdoc.Metric{Name: "cyclomatic_complexity", Value: 17, Threshold: 16, Unit: "count"}
	}
	doc := obsdoc.New(obsdoc.Analyzer{ID: id, Ecosystem: "any"}, obsdoc.Component{ID: t.Key, Version: "1"}, obsdoc.Component{ID: t.Key + "-stub", Version: "1"}, t.Key, j.Snapshot.Digest)
	doc.Module.Dir, doc.Tool.Name = ".", t.Key
	version := "1.0.0"
	doc.Tool.Version, doc.Tool.VersionTested = &version, true
	doc.RulePack = obsdoc.RulePack{ID: "stub", Version: "1", Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressHonored,
		ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	stdout := []byte("record\n")
	code := 0
	doc.Executions = []obsdoc.Execution{{Name: t.Key + ".run", Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Command: analysis.ManifestCommand{Argv: []string{t.Key}, Dir: ".", EnvKeys: []string{}},
		Stdout:  obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(stdout), CapturedBytes: int64(len(stdout)), TotalBytes: int64(len(stdout))}, Stderr: obsdoc.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}}}
	var sha string
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Path == "gateway.go" {
			sha = e.SHA256
		}
	}
	p := "gateway.go"
	doc.Observations = []obsdoc.Observation{{ID: obsdoc.ObservationID(id, ".", p, rule, "gateway.go:17", "gateway.go:17", msg, 0), RuleID: rule, Category: category, RawMessage: msg,
		Path: &p, Location: &obsdoc.Location{Path: p, StartLine: 17, EndLine: 17, ColumnUnit: "byte", BlobSHA256: sha}, LocationState: obsdoc.LocationResolved,
		Related: []obsdoc.Related{}, RawRef: obsdoc.RawRef{Execution: t.Key + ".run", Stream: "stdout", StreamSHA256: obsdoc.SHA256Hex(stdout), ByteEnd: int64(len(stdout))}, Unit: p, Metric: metric}}
	doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: obsdoc.ExitSuccessWithFindings}
	if metric != nil {
		doc.Outcome.ExitClass = obsdoc.ExitSuccessWithoutFindings
		doc.Limitations = []string{"metrics_not_defects"}
	}
	doc.Coverage.ProcessedPaths = []string{p}
	doc.Parse.OK = true
	if t.Key == secretsTaskKey && w.mode != "" {
		doc.Observations = []obsdoc.Observation{}
		doc.Parse.OK = false
		if w.mode == "failed" {
			reason := "scan_incomplete"
			doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskFailed, Reason: &reason, ExitClass: obsdoc.ExitExecutionError}
			doc.Coverage.ProcessedPaths = nil
		} else {
			reason := "tool_not_configured"
			doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskUnsupported, Reason: &reason, ExitClass: obsdoc.ExitNotRun}
			doc.Executions, doc.Coverage.ProcessedPaths = []obsdoc.Execution{}, []string{}
		}
	}
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		panic(err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}}
}

// TC-118 (docs/ANALYZERS.md): a file a secret analyzer reported reaches no model
// input: not as code context, not as an observation window of any tool and
// not as a verification expansion. The detection itself (fixed message,
// path, line) is interpreted; a structure metric never is.
func TestRolePackWithholdsSecretFiles(t *testing.T) {
	value := "deploy-token-" + strings.Repeat("q7", 10) // not a Sensitive shape
	j := modulesJob(t, func(root string) {
		p := filepath.Join(root, "gateway.go")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, root, "gateway.go", string(b)+"\nconst deployToken = \""+value+"\"\n")
	})
	script := validScript(t)
	script["interpret"] = func(int, opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"hypotheses": []any{}, "limits": []any{}})
	}
	f := &roleCaller{script: script}
	_, res := runRole(t, j, screenStub{}, f, "1.27.4")
	if slices.Contains(readRole(t, j, "investigate").Limitations, limNotSecretScreened) {
		t.Fatal("screened inputs marked unscreened")
	}
	if res[secretsTaskKey].Status != domain.TaskSucceeded || res["lizard"].Status != domain.TaskSucceeded {
		t.Fatalf("static tasks %+v %+v", res[secretsTaskKey], res["lizard"])
	}
	if len(f.requests) == 0 {
		t.Fatal("no model request")
	}
	var in interpretInput
	for _, q := range f.requests {
		if strings.Contains(string(q.Input), value) || strings.Contains(q.Instructions, value) {
			t.Fatalf("%s request holds the planted value", q.Role)
		}
		if q.Role == "interpret" {
			in = decodeAs[interpretInput](t, q)
		}
	}
	var rules []string
	for _, o := range in.Observations {
		rules = append(rules, o.TaskKey+"/"+o.RuleID)
		if o.Path == "gateway.go" && len(o.EvidenceIDs) != 0 {
			t.Fatalf("window on a withheld file: %+v", o)
		}
		// A tool message on a withheld file may quote the value.
		if o.Path == "gateway.go" && o.Message != "(omitted by the platform)" {
			t.Fatalf("message of a withheld file reached the model: %+v", o)
		}
	}
	slices.Sort(rules)
	if !slices.Equal(rules, []string{"go-vet/unusedresult", secretsTaskKey + "/aws-access-token"}) {
		t.Fatalf("interpret observations %v", rules)
	}
	for _, role := range []string{"investigate", "interpret"} {
		rec := readRole(t, j, role)
		if !slices.Contains(rec.Limitations, limSecretWithheld) {
			t.Fatalf("%s limitations %v", role, rec.Limitations)
		}
		for _, e := range rec.Evidence {
			if e.Path == "gateway.go" {
				t.Fatalf("%s evidence on a withheld file", role)
			}
		}
	}
}

// plantedJob is the modules fixture with a value the Sensitive pattern does not
// recognize appended to gateway.go (inside the first 50 lines).
func plantedJob(t *testing.T) (analysis.Job, string) {
	value := "deploy-token-" + strings.Repeat("z9", 10)
	j := modulesJob(t, func(root string) {
		b, err := os.ReadFile(filepath.Join(root, "gateway.go"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, root, "gateway.go", string(b)+"\nconst deployToken = \""+value+"\"\n")
	})
	return j, value
}

// The secrets screen is three-way (screen.go). Not run (the stand-in's
// not_run envelope): the role pack runs unscreened and every record says so. Ran but
// unusable (a failed scan, or no envelope at all, also what a role would
// see if it ever ran before the secrets task): every role is skipped with
// secret_screen_failed and no request is made.
func TestRolePackSecretScreenCases(t *testing.T) {
	for _, mode := range []string{"not_run", "failed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			j, value := plantedJob(t)
			script := validScript(t)
			script["interpret"] = func(int, opencode.Request) opencode.Reply {
				return asJSON(map[string]any{"hypotheses": []any{}, "limits": []any{}})
			}
			f := &roleCaller{script: script}
			plan, res := runRole(t, j, screenStub{mode: mode}, f, "1.27.4")
			if mode != "not_run" {
				if len(f.requests) != 0 {
					t.Fatalf("%d model requests after an unusable scan", len(f.requests))
				}
				for _, p := range plan {
					if strings.HasPrefix(p.Key, "review.") && (res[p.Key].Status != domain.TaskSkippedPolicy || res[p.Key].Reason != reasonSecretScreenFailed) {
						t.Fatalf("%s: %+v", p.Key, res[p.Key])
					}
				}
				return
			}
			sent := false
			for _, q := range f.requests {
				sent = sent || strings.Contains(string(q.Input), value)
				if strings.Contains(string(q.Input), limNotSecretScreened) {
					t.Fatal("the limitation changed a model input")
				}
			}
			if !sent {
				t.Fatal("unscreened inputs should carry the file (the limitation is the signal)")
			}
			for _, role := range []string{"environment", "documents", "investigate", "interpret", "verify"} {
				if !slices.Contains(readRole(t, j, role).Limitations, limNotSecretScreened) {
					t.Fatalf("%s record lacks %s", role, limNotSecretScreened)
				}
			}
		})
	}
}

// The legacy pack (review-v1) applies the same screen: its corpus leaves
// the reported file out, an unusable scan skips the role without a request,
// and an unscreened corpus is recorded (never in the pinned model input).
func TestLegacyPackSecretScreen(t *testing.T) {
	for _, mode := range []string{"", "not_run", "failed", "missing"} {
		t.Run("mode="+mode, func(t *testing.T) {
			j, value := plantedJob(t)
			f := &roleCaller{script: map[string]func(int, opencode.Request) opencode.Reply{"context": func(_ int, q opencode.Request) opencode.Reply {
				in := decodeInput(t, q)
				return ok(map[string]any{"facts": []any{map[string]any{"kind": "observed", "statement": "게이트웨이는 실패한 호출을 다시 시도한다.", "evidence_ids": []string{in.Scope.Excerpts[0].ID}}}})
			}}}
			h := Handler{Static: screenStub{mode: mode}, Caller: f}
			plan, err := h.Plan(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range plan {
				if !strings.HasPrefix(p.Key, "review.") {
					h.Run(t.Context(), j, p, nil)
				}
			}
			res := h.Run(t.Context(), j, analysis.TaskSpec{Key: "review.context"}, nil)
			switch mode {
			case "failed", "missing":
				if res.Status != domain.TaskSkippedPolicy || res.Reason != reasonSecretScreenFailed || len(f.requests) != 0 {
					t.Fatalf("%+v requests %d", res, len(f.requests))
				}
				return
			}
			if res.Status != domain.TaskSucceeded || len(f.requests) != 1 {
				t.Fatalf("%+v requests %d", res, len(f.requests))
			}
			in := decodeInput(t, f.requests[0])
			hasGateway := slices.ContainsFunc(in.Scope.Excerpts, func(x Excerpt) bool { return x.Path == "gateway.go" })
			rec, err := readRecord(j, "context")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "" {
				if hasGateway || strings.Contains(string(f.requests[0].Input), value) || slices.Contains(rec.Limitations, limNotSecretScreened) || !slices.Contains(rec.Limitations, limSecretWithheld) {
					t.Fatalf("screened corpus: gateway %v limitations %v", hasGateway, rec.Limitations)
				}
				return
			}
			if !hasGateway || !slices.Contains(rec.Limitations, limNotSecretScreened) || strings.Contains(string(f.requests[0].Input), limNotSecretScreened) {
				t.Fatalf("unscreened corpus: gateway %v limitations %v", hasGateway, rec.Limitations)
			}
		})
	}
}
