package obsdoc

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// The example is a real staticcheck 2026.2.2 envelope (docs/39 §8).
func example(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../../examples/observations-staticcheck.json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func encode(t *testing.T, d map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lookupOf is the manifest the example was produced against, rebuilt from
// its own locations and processed paths.
func lookupOf(d map[string]any) func(string) (string, bool) {
	index := map[string]string{}
	for _, p := range d["coverage"].(map[string]any)["processed_paths"].([]any) {
		index[p.(string)] = strings.Repeat("b", 64)
	}
	for _, o := range d["observations"].([]any) {
		if l, ok := o.(map[string]any)["location"].(map[string]any); ok {
			index[l["path"].(string)] = l["blob_sha256"].(string)
		}
	}
	return func(p string) (string, bool) { s, ok := index[p]; return s, ok }
}

func TestDecodeExampleRoundTrip(t *testing.T) {
	d := example(t)
	want := Expect{TaskKey: "staticcheck", Snapshot: d["snapshot_manifest_digest"].(string), Lookup: lookupOf(d)}
	doc, err := Decode(encode(t, d), want)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Analyzer.ID != "go.staticcheck" || doc.Analyzer.Required || doc.Outcome.ExitClass != ExitSuccessWithFindings || len(doc.Observations) != 7 {
		t.Fatalf("decoded %+v %+v", doc.Analyzer, doc.Outcome)
	}
	b, err := Marshal(doc, want)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := domain.CanonicalJSON(encode(t, d))
	if string(b) != string(c) {
		t.Fatal("Marshal(Decode(x)) is not the canonical form of x")
	}
	if _, err := Decode(encode(t, d), Expect{TaskKey: "staticcheck.m01"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("other task key accepted")
	}
	if _, err := Decode(encode(t, d), Expect{Snapshot: strings.Repeat("f", 64)}); !errors.Is(err, ErrInvalid) {
		t.Fatal("other snapshot accepted")
	}
}

func obs0(d map[string]any) map[string]any {
	return d["observations"].([]any)[0].(map[string]any)
}

// Every rule of Validate rejects a document that claims more than it records.
func TestValidateRejects(t *testing.T) {
	cases := map[string]func(d map[string]any){
		"go vet kind":                func(d map[string]any) { d["kind"] = "go_vet_observations" },
		"unknown field":              func(d map[string]any) { d["extra"] = 1 },
		"hyphen analyzer id":         func(d map[string]any) { d["analyzer"].(map[string]any)["id"] = "go.static-check" },
		"unknown ecosystem":          func(d map[string]any) { d["analyzer"].(map[string]any)["ecosystem"] = "java" },
		"success with partial parse": func(d map[string]any) { d["outcome"].(map[string]any)["partial_parse"] = true },
		"success with reason":        func(d map[string]any) { d["outcome"].(map[string]any)["reason"] = "x" },
		"success wrong exit class":   func(d map[string]any) { d["outcome"].(map[string]any)["exit_class"] = ExitSuccessWithoutFindings },
		"success without processed":  func(d map[string]any) { d["coverage"].(map[string]any)["processed_paths"] = nil },
		"success with tool error": func(d map[string]any) {
			d["tool_errors"] = []any{map[string]any{"execution": "staticcheck.check", "kind": "typecheck", "unit": "u", "message": "m", "path": nil, "line": 0, "column": 0}}
			d["parse"].(map[string]any)["tool_errors"] = 1
		},
		"failed with success class": func(d map[string]any) {
			d["outcome"].(map[string]any)["task_status"] = "failed"
			d["outcome"].(map[string]any)["reason"] = "x"
		},
		"not run with observations": func(d map[string]any) {
			d["outcome"] = map[string]any{"task_status": "unsupported", "reason": "tool_not_configured", "exit_class": ExitNotRun, "partial_parse": false}
		},
		"unclean execution on success": func(d map[string]any) {
			d["executions"].([]any)[0].(map[string]any)["stdout"].(map[string]any)["truncated"] = true
		},
		"node path in argv": func(d map[string]any) {
			e := d["executions"].([]any)[4].(map[string]any)["command"].(map[string]any)
			e["argv"] = append(e["argv"].([]any), "/home/u/x")
		},
		"path in argv0": func(d map[string]any) {
			d["executions"].([]any)[0].(map[string]any)["command"].(map[string]any)["argv"].([]any)[0] = "bin/go"
		},
		"absolute target property": func(d map[string]any) {
			d["target"].(map[string]any)["properties"].(map[string]any)["goroot"] = "/usr/lib/go"
		},
		"absolute rule selection":   func(d map[string]any) { d["rule_pack"].(map[string]any)["selection"] = []any{"-conf", "C:\\x"} },
		"unsorted limitations":      func(d map[string]any) { d["limitations"] = []any{"b_x", "a_x"} },
		"tested without version":    func(d map[string]any) { d["tool"].(map[string]any)["version"] = nil },
		"raw ref other stream sha":  func(d map[string]any) { obs0(d)["raw_ref"].(map[string]any)["stream_sha256"] = strings.Repeat("e", 64) },
		"raw ref past captured":     func(d map[string]any) { obs0(d)["raw_ref"].(map[string]any)["byte_end"] = 1 << 20 },
		"raw ref unknown exec":      func(d map[string]any) { obs0(d)["raw_ref"].(map[string]any)["execution"] = "staticcheck.other" },
		"empty range":               func(d map[string]any) { r := obs0(d)["raw_ref"].(map[string]any); r["byte_end"] = r["byte_start"] },
		"resolved without location": func(d map[string]any) { obs0(d)["location"] = nil },
		"location path differs":     func(d map[string]any) { obs0(d)["path"] = "find/other.go" },
		"unknown category":          func(d map[string]any) { obs0(d)["category"] = "style" },
		"duplicate observation id": func(d map[string]any) {
			d["observations"].([]any)[1].(map[string]any)["id"] = obs0(d)["id"]
		},
		"unresolved count":  func(d map[string]any) { d["parse"].(map[string]any)["unresolved_observations"] = 1 },
		"observation count": func(d map[string]any) { d["parse"].(map[string]any)["observations"] = 6 },
		"drifted file linked": func(d map[string]any) {
			d["workspace_drift"] = []any{"find/find.go"}
			d["outcome"] = map[string]any{"task_status": "failed", "reason": "workspace_drift", "exit_class": ExitExecutionError, "partial_parse": true}
			d["coverage"].(map[string]any)["processed_paths"] = nil
		},
		"manifest blob differs": func(d map[string]any) {
			obs0(d)["location"].(map[string]any)["blob_sha256"] = strings.Repeat("0", 64)
		},
		"processed path outside manifest": func(d map[string]any) {
			d["coverage"].(map[string]any)["processed_paths"] = []any{"clean/c.go", "find/find.go", "find/find_test.go", "zz/none.go"}
		},
		"processed paths unsorted": func(d map[string]any) {
			d["coverage"].(map[string]any)["processed_paths"] = []any{"find/find.go", "clean/c.go"}
		},
		"related unresolved with location": func(d map[string]any) {
			for _, o := range d["observations"].([]any) {
				for _, r := range o.(map[string]any)["related"].([]any) {
					r.(map[string]any)["location_state"] = LocationNoPosition
				}
			}
		},
	}
	for name, edit := range cases {
		d := example(t)
		lookup := lookupOf(d)
		edit(d)
		if _, err := Decode(encode(t, d), Expect{Lookup: lookup}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted (%v)", name, err)
		}
	}
}

// A not-run document records nothing processed (known zero) and no
// observation; a failed one may keep what was parsed with unknown coverage.
func TestValidateNotRunAndFailed(t *testing.T) {
	d := example(t)
	d["outcome"] = map[string]any{"task_status": "unsupported", "reason": "tool_not_configured", "exit_class": ExitNotRun, "partial_parse": false}
	d["observations"] = []any{}
	d["parse"].(map[string]any)["observations"] = 0
	d["parse"].(map[string]any)["ok"] = false
	cov := d["coverage"].(map[string]any)
	cov["processed_paths"] = []any{}
	if _, err := Decode(encode(t, d), Expect{}); err != nil {
		t.Fatal(err)
	}
	cov["processed_paths"] = nil
	if _, err := Decode(encode(t, d), Expect{}); err == nil {
		t.Fatal("not_run with unknown coverage accepted")
	}
	f := example(t)
	f["outcome"] = map[string]any{"task_status": "failed", "reason": "package_errors", "exit_class": ExitExecutionError, "partial_parse": true}
	f["coverage"].(map[string]any)["processed_paths"] = nil
	f["parse"].(map[string]any)["ok"] = false
	if _, err := Decode(encode(t, f), Expect{}); err != nil {
		t.Fatal(err)
	}
}

func TestObservationIDIsContentDerived(t *testing.T) {
	a := ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 0)
	if a != ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 0) || !observationID.MatchString(a) {
		t.Fatal(a)
	}
	for _, b := range []string{
		ObservationID("go.vet", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 0),
		ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 1),
		ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "n", 0),
	} {
		if a == b {
			t.Fatal("ID ignores a component")
		}
	}
	if !AnalyzerIDPattern.MatchString("cpp.clang_tidy") || AnalyzerIDPattern.MatchString("staticcheck") || AnalyzerIDPattern.MatchString("go.static-check") {
		t.Fatal("analyzer id pattern")
	}
	if SafeText("a\x00b", 10) || !SafeText("a\tb\nc", 10) || SafeText(strings.Repeat("x", 11), 10) {
		t.Fatal("SafeText")
	}
}
