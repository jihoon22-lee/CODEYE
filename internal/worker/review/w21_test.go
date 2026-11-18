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

// w21Stub is the go vet stub plus the W21 envelopes (docs/45): "lizard"
// with one complexity metric on gateway.go and the secrets task with one
// detection on gateway.go, where the test planted a value the platform's
// Sensitive pattern does not recognize.
type w21Stub struct{}

const w21SecretKey = "secrets"

func (w21Stub) Info() analysis.HandlerInfo {
	return analysis.HandlerInfo{Name: pack.CompositeName, Version: "stub", Capability: pack.Capability}
}

func (w21Stub) Plan(ctx context.Context, j analysis.Job) ([]analysis.TaskSpec, error) {
	plan, _ := vetStub{}.Plan(ctx, j)
	for _, k := range []string{"lizard", w21SecretKey} {
		plan = append(plan, analysis.TaskSpec{Key: k, Title: k, Stage: domain.StageStaticAnalysis})
	}
	return plan, nil
}

func (w21Stub) Run(ctx context.Context, j analysis.Job, t analysis.TaskSpec, r executor.Runner) analysis.TaskResult {
	if pack.Owns("go-vet", t.Key) {
		return vetStub{obs: gatewayObservation}.Run(ctx, j, t, r)
	}
	id, rule, msg, category := "any.lizard", "cyclomatic_complexity", "Forward(...) at gateway.go:10: NCSS 5, CCN 17", "maintainability"
	var metric *obsdoc.Metric
	if t.Key == w21SecretKey {
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
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		panic(err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Artifacts: []analysis.ArtifactFile{art}}
}

// TC-118 (docs/45): a file a secret analyzer reported reaches no model
// input: not as code context, not as an observation window of any tool and
// not as a verification expansion. The detection itself (fixed message,
// path, line) is interpreted; a structure metric never is.
func TestR1WithholdsSecretFiles(t *testing.T) {
	value := "deploy-token-" + strings.Repeat("q7", 10) // not a Sensitive shape
	j := w15Job(t, func(root string) {
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
	f := &r1Caller{script: script}
	_, res := runR1(t, j, w21Stub{}, f, "1.27.4")
	if res[w21SecretKey].Status != domain.TaskSucceeded || res["lizard"].Status != domain.TaskSucceeded {
		t.Fatalf("static tasks %+v %+v", res[w21SecretKey], res["lizard"])
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
	}
	slices.Sort(rules)
	if !slices.Equal(rules, []string{"go-vet/unusedresult", w21SecretKey + "/aws-access-token"}) {
		t.Fatalf("interpret observations %v", rules)
	}
	for _, role := range []string{"investigate", "interpret"} {
		rec := readR1(t, j, role)
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
